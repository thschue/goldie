package app

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// #636: GOLDIE_MODE=ui serves the SPA and reverse-proxies /api, /openapi.* and
// /swagger-ui to GOLDIE_UPSTREAM_ADDR. It does not mount /mcp, and /readyz
// lists one `upstream` check. The caller's credential passes through (ADR
// 0003): the UI-mode server holds no token of its own.
//
// The settings matrix (which variables stop startup in which mode) is
// TestUIModeStartupValidation in internal/platform.

const (
	envMode         = "GOLDIE_MODE"
	envUpstreamAddr = "GOLDIE_UPSTREAM_ADDR"
)

// uiUpstream is a real goldie server (mode all, so /mcp is live there) with a
// signing key and one operator token, on a loopback address.
func uiUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := sessionConfig(t, "ci:operator:"+operatorToken, map[string]string{
		"GOLDIE_SESSION_KEY":         sessionKey,
		platform.EnvSwaggerUIEnabled: "true",
	})
	h, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("upstream PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// uiModeHandler is the public handler in mode ui in front of upstream.
func uiModeHandler(t *testing.T, upstream string) *Public {
	t.Helper()
	cfg := loadedConfig(t, map[string]string{envMode: "ui", envUpstreamAddr: upstream})
	pub, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("PublicHandler in mode ui: %v", err)
	}
	return pub
}

// uiModeServer serves uiModeHandler on a loopback address.
func uiModeServer(t *testing.T, upstream string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(uiModeHandler(t, upstream))
	t.Cleanup(srv.Close)
	return srv
}

// deadAddr is the base URL of a server that was listening and is closed.
func deadAddr(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

func TestModeUIServesTheSPAAndProxiesToTheUpstream(t *testing.T) {
	upstream := uiUpstream(t)
	srv := uiModeServer(t, upstream.URL)

	// /: the SPA.
	resp, body := uiDo(t, http.MethodGet, srv.URL+"/", nil)
	assertUIPage(t, "GET /", resp, body, uiIndex(t))

	// /api, /openapi.* and /swagger-ui answer what the upstream answers.
	for _, p := range []string{"/api/v1/search?q=kubernetes", "/openapi.json", "/openapi.yaml", "/swagger-ui"} {
		want, wantBody := uiDo(t, http.MethodGet, upstream.URL+p, bearer(operatorToken))
		got, gotBody := uiDo(t, http.MethodGet, srv.URL+p, bearer(operatorToken))
		if want.StatusCode != http.StatusOK {
			t.Fatalf("fixture: upstream GET %s: status %d", p, want.StatusCode)
		}
		if got.StatusCode != http.StatusOK || string(gotBody) != string(wantBody) {
			t.Errorf("GET %s through the proxy: status %d, %d bytes, want the upstream's 200 and %d bytes", p, got.StatusCode, len(gotBody), len(wantBody))
		}
	}

	// The caller's credential decides: no token is stored here, so none is sent.
	resp, body = uiDo(t, http.MethodGet, srv.URL+"/api/v1/search?q=kubernetes", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api without a token: status %d, want the upstream's 401", resp.StatusCode)
	}
	assertEnvelope(t, body, "")
	resp, _ = uiDo(t, http.MethodGet, srv.URL+"/api/v1/search?q=kubernetes", bearer("wrong-token-0123456789abcdef0123456789"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api with a wrong token: status %d, want 401", resp.StatusCode)
	}
}

func TestModeUIDoesNotMountMCP(t *testing.T) {
	upstream := uiUpstream(t)
	if resp, _ := uiDo(t, http.MethodPost, upstream.URL+"/mcp", bearer(operatorToken)); resp.StatusCode == http.StatusNotFound {
		t.Fatal("fixture: the upstream does not serve /mcp, so a proxied /mcp would be indistinguishable")
	}
	srv := uiModeServer(t, upstream.URL)
	for _, p := range []string{"/mcp", "/mcp/"} {
		resp, body := uiDo(t, http.MethodPost, srv.URL+p, bearer(operatorToken))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s: status %d, want 404", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "not_found")
	}
}

// Signing in through the proxy gives a working session: the upstream signs
// the cookie for the Host and Origin the browser used.
func TestModeUISigningInThroughTheProxyGivesAWorkingSession(t *testing.T) {
	srv := uiModeServer(t, uiUpstream(t).URL)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+sessionPath,
		strings.NewReader(mustJSON(t, map[string]string{"token": operatorToken})))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // test server URL
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in through the proxy: status %d, want 200: %s", resp.StatusCode, body)
	}
	cookie := theCookie(t, resp)

	resp, body = uiDo(t, http.MethodGet, srv.URL+sessionPath, withCookie(cookie))
	if resp.StatusCode != http.StatusOK || readSession(t, body).Role != "operator" {
		t.Errorf("GET session with the cookie through the proxy: status %d, body %s, want the operator session", resp.StatusCode, body)
	}
	resp, body = uiDo(t, http.MethodGet, srv.URL+sessionSearch, withCookie(cookie))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET search with the session cookie through the proxy: status %d, want 200: %s", resp.StatusCode, body)
	}
}

// seen is what a fake upstream saw of one request.
type seen struct {
	host, origin, cookie string
	header               http.Header
}

// recordingUpstream answers every request with two fixed Set-Cookie headers
// and records what reached it.
func recordingUpstream(t *testing.T) (*httptest.Server, func() seen) {
	t.Helper()
	var mu sync.Mutex
	var last seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = seen{host: r.Host, origin: r.Header.Get("Origin"), cookie: r.Header.Get("Cookie"), header: r.Header.Clone()}
		mu.Unlock()
		w.Header().Add("Set-Cookie", "__Host-goldie_session=abc.def; Path=/; Secure; HttpOnly; SameSite=Strict")
		w.Header().Add("Set-Cookie", "other=1; Path=/api; HttpOnly")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() seen { mu.Lock(); defer mu.Unlock(); return last }
}

func TestModeUIPassesHostOriginCookieAndSetCookieUnchanged(t *testing.T) {
	upstream, got := recordingUpstream(t)
	srv := uiModeServer(t, upstream.URL)
	u, _ := url.Parse(srv.URL)

	resp, _ := uiDo(t, http.MethodGet, srv.URL+"/api/v1/session", func(r *http.Request) {
		r.Header.Set("Origin", srv.URL)
		r.Header.Set("Cookie", "__Host-goldie_session=abc.def; theme=dark")
	})
	s := got()
	if s.host != u.Host {
		t.Errorf("upstream saw Host %q, want the caller's %q", s.host, u.Host)
	}
	if s.origin != srv.URL {
		t.Errorf("upstream saw Origin %q, want %q", s.origin, srv.URL)
	}
	if s.cookie != "__Host-goldie_session=abc.def; theme=dark" {
		t.Errorf("upstream saw Cookie %q, want it unchanged", s.cookie)
	}
	want := []string{
		"__Host-goldie_session=abc.def; Path=/; Secure; HttpOnly; SameSite=Strict",
		"other=1; Path=/api; HttpOnly",
	}
	if got := resp.Header.Values("Set-Cookie"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Set-Cookie = %q, want the upstream's %q unchanged", got, want)
	}
}

// Forwarding headers are never trusted and never added: the upstream sees no
// X-Forwarded-* or Forwarded header, even when the caller sends them.
func TestModeUINeverTrustsOrAddsForwardingHeaders(t *testing.T) {
	upstream, got := recordingUpstream(t)
	srv := uiModeServer(t, upstream.URL)
	for _, spoof := range []bool{false, true} {
		uiDo(t, http.MethodGet, srv.URL+"/api/v1/search?q=x", func(r *http.Request) {
			if spoof {
				r.Header.Set("X-Forwarded-For", "203.0.113.9")
				r.Header.Set("X-Forwarded-Host", "evil.example.com")
				r.Header.Set("X-Forwarded-Proto", "https")
				r.Header.Set("Forwarded", "for=203.0.113.9;host=evil.example.com")
			}
		})
		h := got().header
		for name := range h {
			if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") || strings.EqualFold(name, "Forwarded") {
				t.Errorf("spoofed=%v: upstream saw %s: %q", spoof, name, h.Values(name))
			}
		}
	}
}

// The activity stream is flushed through the proxy as it is written, not when
// the upstream finishes.
func TestModeUIFlushesTheActivityStream(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"id\":1}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(release) })
	srv := uiModeServer(t, upstream.URL)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+activityPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // test server URL
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "data: {\"id\":1}\n" {
			t.Errorf("first line through the proxy = %q, want the event", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5s: the proxy does not flush a stream while the upstream holds it open")
	}
}

func TestModeUIAnswers502WithoutTheUpstreamAddress(t *testing.T) {
	dead := deadAddr(t)
	srv := uiModeServer(t, dead)
	u, _ := url.Parse(dead)
	for _, p := range []string{"/api/v1/search?q=kubernetes", "/openapi.json", "/swagger-ui"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, bearer(operatorToken))
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("GET %s with the upstream down: status %d, want 502", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "")
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s: Content-Type %q, want JSON", p, ct)
		}
		var all strings.Builder
		all.Write(body)
		for k, vs := range resp.Header {
			all.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
		}
		for _, leak := range []string{dead, u.Host, u.Port(), "refused", "dial"} {
			if strings.Contains(all.String(), leak) {
				t.Errorf("GET %s: the 502 answer contains %q: %s", p, leak, all.String())
			}
		}
	}
	// The SPA does not need the upstream.
	resp, body := uiDo(t, http.MethodGet, srv.URL+"/", nil)
	assertUIPage(t, "GET / with the upstream down", resp, body, uiIndex(t))
}

// uiReadyz starts pub on a registry and returns what the admin /readyz says.
func uiReadyz(t *testing.T, pub *Public) (int, string, []readyzCheck) {
	t.Helper()
	reg := platform.NewRegistry(0)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startReturns(t, pub, ctx, reg)
	return readyz(t, adminServer(t, publicConfig(t), pub, reg))
}

func TestModeUIReadyzHasOneUpstreamCheck(t *testing.T) {
	t.Run("an unauthenticated 401 envelope counts as reachable", func(t *testing.T) {
		upstream := uiUpstream(t)
		code, raw, checks := uiReadyz(t, uiModeHandler(t, upstream.URL))
		if code != http.StatusOK || len(checks) != 1 || checks[0] != (readyzCheck{Name: "upstream", Status: "ok"}) {
			t.Errorf("/readyz = %d %s, want 200 with exactly [upstream ok]", code, raw)
		}
	})
	t.Run("an unreachable upstream fails the check without its address", func(t *testing.T) {
		dead := deadAddr(t)
		u, _ := url.Parse(dead)
		code, raw, checks := uiReadyz(t, uiModeHandler(t, dead))
		if code != http.StatusServiceUnavailable || len(checks) != 1 || checks[0].Name != "upstream" || checks[0].Status == "ok" {
			t.Errorf("/readyz = %d %s, want 503 with exactly one failing upstream check", code, raw)
		}
		for _, leak := range []string{dead, u.Host, "refused"} {
			if strings.Contains(raw, leak) {
				t.Errorf("/readyz body contains %q: %s", leak, raw)
			}
		}
	})
}

// A hostile upstream address is a startup error that names the variable and
// never the value. Which stage refuses it (LoadConfig or PublicHandler) is not
// pinned.
func TestModeUIRefusesAnUnsafeUpstreamAddr(t *testing.T) {
	for name, addr := range map[string]string{
		"credentials": "http://user:hunter2@127.0.0.1:9",
		"scheme":      "ftp://hunter2.example.com",
		"plain http":  "http://hunter2.example.com",
	} {
		env := map[string]string{envMode: "ui", envUpstreamAddr: addr}
		cfg, err := platform.LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
		if err == nil {
			_, err = PublicHandler(cfg, nil)
		}
		if err == nil {
			t.Errorf("%s: want a startup error", name)
			continue
		}
		if !strings.Contains(err.Error(), envUpstreamAddr) || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: error %q, want it to name %s and not the value", name, err, envUpstreamAddr)
		}
	}
}
