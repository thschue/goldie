package cli_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance tests for #150 (from #155): the CLI client follows no redirects,
// so a redirecting server can never pass the token on, for the cases where
// Go's default client would forward Authorization: another host name
// that is a subdomain, the same host on another port, and https to http.
//
// The cases need host names and a trusted TLS server, which only an injected
// http.Client can give, so they run at the cli.Run boundary with Env.Client.
// The client the test injects follows redirects as any http.Client does; the
// CLI must still refuse them, as it caps answers on an injected client too.

const redirectToken = "redirect-test-token-0123456789abcdef01234"

// target records every request it receives.
type target struct {
	mu    sync.Mutex
	auths []string
	srv   *httptest.Server
}

func (tg *target) requests() []string {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return append([]string(nil), tg.auths...)
}

func newTarget(t *testing.T) *target {
	t.Helper()
	tg := &target{}
	tg.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tg.mu.Lock()
		tg.auths = append(tg.auths, r.Host+" Authorization="+r.Header.Get("Authorization"))
		tg.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/openapi.json":
			_, _ = w.Write([]byte(`{"info":{"title":"Target","version":"1"}}`))
		case r.URL.Query().Has("q"):
			_, _ = w.Write([]byte(`{"results":[]}`))
		default:
			_, _ = w.Write([]byte(`{"id":"x","name":"X","keywords":["k"],"content":"body"}`))
		}
	}))
	t.Cleanup(tg.srv.Close)
	return tg
}

// redirector answers every request with a 307 (which keeps the method) to
// location plus the request's path.
func redirector(location string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", location+r.URL.RequestURI())
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
}

func port(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

// loopbackClient dials every host name to the loopback port it names, and
// trusts the TLS test certificate, which covers example.com and *.example.com.
func loopbackClient(t *testing.T, tlsSrv *httptest.Server) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(tlsSrv.Certificate())
	var d net.Dialer
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, p, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			return d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", p))
		},
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
}

func noEnv(string) (string, bool) { return "", false }

func TestCLIFollowsNoRedirectThatWouldCarryTheToken(t *testing.T) {
	tg := newTarget(t)
	tp := port(t, tg.srv)

	// The TLS test certificate is the same for every TLS test server, so a
	// client that trusts one trusts them all.
	trust := httptest.NewTLSServer(nil)
	t.Cleanup(trust.Close)
	client := loopbackClient(t, trust)
	from := func(secure bool, to string) (*httptest.Server, string) {
		srv := httptest.NewUnstartedServer(redirector(to))
		scheme := "http"
		if secure {
			srv.StartTLS()
			scheme = "https"
		} else {
			srv.Start()
		}
		t.Cleanup(srv.Close)
		return srv, scheme + "://example.com:" + port(t, srv)
	}

	type redirectCase struct {
		name  string
		start string // --addr
		to    string // where the redirect points
	}
	var cases []redirectCase
	for _, c := range []struct {
		name   string
		secure bool
		to     string
	}{
		{"same host, other port", false, "http://example.com:" + tp},
		{"subdomain", false, "http://api.example.com:" + tp},
		{"https to http", true, "http://example.com:" + tp},
		{"https to http on a subdomain", true, "http://sub.example.com:" + tp},
	} {
		_, start := from(c.secure, c.to)
		cases = append(cases, redirectCase{c.name, start, c.to})
	}

	// Guard: with the client's own redirect policy, each case does carry the
	// token to the target, so the assertions below can see a leak.
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodGet, c.start+"/api/v1/articles?q=guard", nil)
		req.Header.Set("Authorization", "Bearer "+redirectToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("guard %s: %v", c.name, err)
		}
		_ = resp.Body.Close()
		got := tg.requests()
		if len(got) != 1 || got[0] != c.to[len("http://"):]+" Authorization=Bearer "+redirectToken {
			t.Fatalf("guard %s: target saw %q; the harness cannot detect a forwarded token", c.name, got)
		}
		tg.mu.Lock()
		tg.auths = nil
		tg.mu.Unlock()
	}

	for _, c := range cases {
		for _, args := range [][]string{
			{"search", "agent", "--token", redirectToken},
			{"lookup", "platform/deploy", "--token", redirectToken},
			{"status", "--token", redirectToken, "--project", "github.com/acme/widgets"},
			{"version"},
		} {
			t.Run(c.name+"/"+args[0], func(t *testing.T) {
				var out bytes.Buffer
				env := cli.Env{Lookup: noEnv, ReadFile: os.ReadFile, Out: &out, Client: client}
				err := cli.Run(context.Background(), append(args, "--addr", c.start), env)
				if got := tg.requests(); len(got) != 0 {
					t.Errorf("the CLI followed the redirect: the target saw %q", got)
				}
				if args[0] != "version" && err == nil {
					t.Errorf("%v on a redirect succeeded with %q, want an error", args, out.String())
				}
				if err != nil {
					for _, bad := range []string{redirectToken, "example.com", tp} {
						if bytes.Contains([]byte(err.Error()), []byte(bad)) {
							t.Errorf("error %q echoes %q", err, bad)
						}
					}
				}
				tg.mu.Lock()
				tg.auths = nil
				tg.mu.Unlock()
			})
		}
	}
}
