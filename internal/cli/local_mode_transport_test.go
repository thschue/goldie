package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #306, round 2: status in local mode, the token probe,
// and redirects, in local and remote mode.

const redirectLeakLocation = "http://leak-host.invalid:1/leak-path?token=leak-location"

const (
	localRedirectWording  = "the embedded server answered with a redirect, which the CLI does not follow"
	remoteRedirectWording = "the server answered with a redirect, which the CLI does not follow"
	remoteAddrAdvice      = "check --addr or GOLDIE_ADDR"
)

// localRun runs args against an embedded server at addr, injected by StartLocal.
func localRun(t *testing.T, addr string, args ...string) error {
	t.Helper()
	var out bytes.Buffer
	env := localEnv(nil, &out)
	env.StartLocal = func(ctx context.Context, dir string) (string, string, func(), error) {
		return addr, localTestToken, func() {}, nil
	}
	return cli.Run(context.Background(), args, env)
}

// requireLocalUnreachable checks an error names the embedded server and carries
// none of the remote advice, the address or the token.
func requireLocalUnreachable(t *testing.T, err error, addr string) {
	t.Helper()
	if err == nil {
		t.Fatal("no error for an unreachable embedded server")
	}
	msg := err.Error()
	host := strings.TrimPrefix(addr, "http://")
	for _, bad := range []string{"--addr", "GOLDIE_ADDR", addr, host, localTestToken} {
		if strings.Contains(msg, bad) {
			t.Errorf("error carries %q: %q", bad, msg)
		}
	}
	low := strings.ToLower(msg)
	if !strings.Contains(low, "embedded server") || !strings.Contains(low, "reach") {
		t.Errorf("error does not say the embedded server could not be reached: %q", msg)
	}
}

// openAPIThen answers /openapi.json with a valid document and every other
// request with next.
func openAPIThen(t *testing.T, next http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/openapi.json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"info":{"title":"Stub","version":"1"}}`))
			return
		}
		next(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func redirectServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", redirectLeakLocation)
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLocalModeStatusAgainstADeadEmbeddedServerNamesIt(t *testing.T) {
	dead := closedAddr(t)
	requireLocalUnreachable(t, localRun(t, dead, "status", "--dir", "/content"), dead)
}

// The openapi.json request succeeds and the token probe (a search) fails.
func TestLocalModeStatusWhenTheTokenProbeFailsNamesTheEmbeddedServer(t *testing.T) {
	srv := openAPIThen(t, func(w http.ResponseWriter, r *http.Request) {
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	})
	requireLocalUnreachable(t, localRun(t, srv.URL, "status", "--dir", "/content"), srv.URL)
}

func TestLocalModeRedirectSaysTheEmbeddedServerRedirected(t *testing.T) {
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect} {
		srv := redirectServer(t, code)
		host := strings.TrimPrefix(srv.URL, "http://")
		for _, args := range [][]string{
			{"search", "x", "--dir", "/content"},
			{"lookup", "platform/deploy", "--dir", "/content"},
			{"status", "--dir", "/content"},
		} {
			t.Run(http.StatusText(code)+"/"+args[0], func(t *testing.T) {
				err := localRun(t, srv.URL, args...)
				if err == nil {
					t.Fatal("no error for a redirect")
				}
				msg := err.Error()
				if !strings.Contains(msg, localRedirectWording) {
					t.Errorf("error = %q, want it to contain %q", msg, localRedirectWording)
				}
				for _, bad := range []string{"--addr", "GOLDIE_ADDR", "leak-host", "leak-path", "leak-location", srv.URL, host, localTestToken} {
					if strings.Contains(msg, bad) {
						t.Errorf("error carries %q: %q", bad, msg)
					}
				}
			})
		}
	}
}

// A redirect on the token probe, after openapi.json answered, is worded the
// same way.
func TestLocalModeStatusRedirectOnTheTokenProbeSaysTheEmbeddedServerRedirected(t *testing.T) {
	srv := openAPIThen(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", redirectLeakLocation)
		w.WriteHeader(http.StatusFound)
	})
	err := localRun(t, srv.URL, "status", "--dir", "/content")
	if err == nil {
		t.Fatal("no error for a redirect")
	}
	msg := err.Error()
	if !strings.Contains(msg, localRedirectWording) {
		t.Errorf("error = %q, want it to contain %q", msg, localRedirectWording)
	}
	for _, bad := range []string{"--addr", "GOLDIE_ADDR", "leak-", srv.URL, localTestToken} {
		if strings.Contains(msg, bad) {
			t.Errorf("error carries %q: %q", bad, msg)
		}
	}
}

// truncatedAnswer promises a body and closes the connection after part of it.
func truncatedAnswer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", "1000")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"results":[`))
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

// An answer that cannot be read to the end is the embedded server's failure
// too, for the command's own request and for the token probe.
func TestLocalModeUnreadableAnswerNamesTheEmbeddedServer(t *testing.T) {
	t.Run("search", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(truncatedAnswer))
		t.Cleanup(srv.Close)
		requireLocalUnreachable(t, localRun(t, srv.URL, "search", "x", "--dir", "/content"), srv.URL)
	})
	// From #311, bundled into #236: lookup reads its answer the same way.
	t.Run("lookup", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(truncatedAnswer))
		t.Cleanup(srv.Close)
		requireLocalUnreachable(t, localRun(t, srv.URL, "lookup", "platform/deploy", "--dir", "/content"), srv.URL)
	})
	t.Run("status token probe", func(t *testing.T) {
		srv := openAPIThen(t, truncatedAnswer)
		requireLocalUnreachable(t, localRun(t, srv.URL, "status", "--dir", "/content"), srv.URL)
	})
}

// Remote mode keeps the origin/main wording and its advice.
func TestRemoteModeRedirectKeepsAddrAdvice(t *testing.T) {
	srv := redirectServer(t, http.StatusFound)
	for _, args := range [][]string{{"search", "x"}, {"lookup", "platform/deploy"}, {"status"}} {
		t.Run(args[0], func(t *testing.T) {
			var out bytes.Buffer
			env := localEnv(map[string]string{platform.EnvToken: localTestToken}, &out)
			err := cli.Run(context.Background(), append(args, "--addr", srv.URL), env)
			if err == nil {
				t.Fatal("no error for a redirect")
			}
			msg := err.Error()
			for _, want := range []string{remoteRedirectWording, remoteAddrAdvice} {
				if !strings.Contains(msg, want) {
					t.Errorf("error = %q, want it to contain %q", msg, want)
				}
			}
			if strings.Contains(msg, "embedded") {
				t.Errorf("remote error names the embedded server: %q", msg)
			}
			for _, bad := range []string{"leak-", srv.URL, localTestToken} {
				if strings.Contains(msg, bad) {
					t.Errorf("error carries %q: %q", bad, msg)
				}
			}
		})
	}
}
