package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
	"github.com/thschue/goldie/internal/ui"
)

// Acceptance tests for #164 at the HTTP seam: PublicHandler serves the
// embedded UI at "/" without a token, behind /api/ and /api, which keep
// their token guard and their not-found envelope.
//
// The UI answers are not API operations, so the spec does not describe them:
// these tests use uiDo, which does not run contracttest.Conforms (the way
// rawGet does for the Swagger UI), and the UI is not in the route table, so
// TestRoutesAndSpecAgree and TestRouterHonoursRegisteredMethods keep checking
// exactly the spec's operations. They must stay green unchanged: the UI is
// registered on the mux as a catch-all, never through publicMux.handle.

const (
	// uiCSP is the UI's Content-Security-Policy, character for character.
	uiCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
		"connect-src 'self'; base-uri 'none'; frame-ancestors 'none'"

	// uiNotBuiltMessage is the startup warning when the placeholder is served.
	uiNotBuiltMessage = "ui not built: serving the placeholder page; run ./run ui"
)

// uiDo sends a request without the spec check and without a token unless
// mutate adds one.
func uiDo(t *testing.T, method, url string, mutate func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(req)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // test server URL
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func uiIndex(t *testing.T) []byte {
	t.Helper()
	b, err := fs.ReadFile(ui.Assets(), "index.html")
	if err != nil {
		t.Fatalf("ui.Assets() has no index.html: %v", err)
	}
	return b
}

// assertUIPage checks resp is the UI's index page with its headers.
func assertUIPage(t *testing.T, what string, resp *http.Response, body []byte, index []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("%s: status %d, want 200: %s", what, resp.StatusCode, body)
		return
	}
	if !bytes.Equal(body, index) {
		t.Errorf("%s: body is not the UI's index.html: %.200s", what, body)
	}
	for name, want := range map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Cache-Control":           "no-cache",
		"Content-Security-Policy": uiCSP,
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := resp.Header.Get(name); got != want {
			t.Errorf("%s: %s = %q, want %q", what, name, got, want)
		}
	}
}

func TestRootServesTheUIWithoutAToken(t *testing.T) {
	srv := newServer(t)
	index := uiIndex(t)
	for _, p := range []string{"/", "/index.html", "/projects", "/projects/github.com/acme/shop", "/articles/agent-loops", "/apix", "/apix/v1"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, nil)
		assertUIPage(t, "GET "+p+" without a token", resp, body, index)
	}
}

func TestRootServesTheUIToATokenHolderToo(t *testing.T) {
	srv := newServer(t)
	resp, body := uiDo(t, http.MethodGet, srv.URL+"/", bearer(testToken))
	assertUIPage(t, "GET / with a token", resp, body, uiIndex(t))
}

func TestUIIsServedBeforeAnyBadTokenCanRejectIt(t *testing.T) {
	// A browser sends no Authorization header for the page. A stale or wrong
	// one must not turn the page into a 401 either: the page holds no data.
	srv := newServer(t)
	resp, body := uiDo(t, http.MethodGet, srv.URL+"/", bearer("wrong-token-0123456789abcdef0123456789"))
	assertUIPage(t, "GET / with a wrong token", resp, body, uiIndex(t))
}

func TestHeadOnTheUIHasHeadersAndNoBody(t *testing.T) {
	srv := newServer(t)
	for _, p := range []string{"/", "/projects"} {
		resp, body := uiDo(t, http.MethodHead, srv.URL+p, nil)
		if resp.StatusCode != http.StatusOK || len(body) != 0 {
			t.Errorf("HEAD %s: status %d, %d body bytes, want 200 and none", p, resp.StatusCode, len(body))
		}
		if got := resp.Header.Get("Content-Security-Policy"); got != uiCSP {
			t.Errorf("HEAD %s: CSP %q, want the UI's", p, got)
		}
	}
}

func TestUIMissingFilesAre404NotHTML(t *testing.T) {
	srv := newServer(t)
	for _, p := range []string{"/assets/x.js", "/favicon.ico", "/assets/missing.css", "/robots.txt", "/.env", "/x.php"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", p, resp.StatusCode)
		}
		if strings.Contains(resp.Header.Get("Content-Type"), "text/html") || strings.Contains(strings.ToLower(string(body)), "<html") {
			t.Errorf("GET %s: a missing file got HTML: %.200s", p, body)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s: X-Content-Type-Options = %q, want nosniff", p, got)
		}
	}
}

// Other methods on a UI path get the 405 envelope with Allow: GET, HEAD, with
// no token: the UI is public, so the refusal does not ask for one.
func TestUIOtherMethodsAre405WithAllowGetHead(t *testing.T) {
	srv := newServer(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		for _, p := range []string{"/", "/projects", "/assets/x.js", "/apix"} {
			resp, body := uiDo(t, method, srv.URL+p, nil)
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want 405", method, p, resp.StatusCode)
				continue
			}
			if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
				t.Errorf("%s %s: Allow = %q, want %q", method, p, got, "GET, HEAD")
			}
			assertEnvelope(t, body, "method_not_allowed")
		}
	}
}

// --- /api and /api/ keep their guard -----------------------------------------

func TestApiPathsStayBehindTheTokenAndNeverFallBackToTheUI(t *testing.T) {
	srv := newServer(t)
	for _, p := range []string{"/api/nope", "/api", "/api/", "/api/v1/nope", "/api/v1", "/api/nope.js", "/api/nope/deeper"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, nil)
		assertUnauthorized(t, "GET "+p+" without a token", resp, body)
		if bytes.Contains(body, uiIndex(t)) {
			t.Errorf("GET %s without a token: the UI leaked into an API path", p)
		}

		resp, body = uiDo(t, http.MethodGet, srv.URL+p, bearer(testToken))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s with a token: status %d, want 404", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "not_found")
		if got := resp.Header.Get("Content-Security-Policy"); got != "" {
			t.Errorf("GET %s: the API's 404 carries the UI's CSP %q", p, got)
		}
	}
}

func TestApiPathsRefuseOtherMethodsLikeBefore(t *testing.T) {
	// POST to an unknown /api/ path is the token's 401 without one and the
	// 404 envelope with one: it is not the UI's 405.
	srv := newServer(t)
	for _, p := range []string{"/api/nope", "/api"} {
		resp, body := uiDo(t, http.MethodPost, srv.URL+p, nil)
		assertUnauthorized(t, "POST "+p+" without a token", resp, body)
		resp, body = uiDo(t, http.MethodPost, srv.URL+p, bearer(testToken))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s with a token: status %d, want 404", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "not_found")
	}
}

func TestApixIsNotAPIPath(t *testing.T) {
	srv := newServer(t)
	index := uiIndex(t)
	for _, p := range []string{"/apix", "/api-docs", "/apis/v1", "/api.json"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, nil)
		if p == "/api.json" {
			if resp.StatusCode != http.StatusNotFound || strings.Contains(string(body), "<html") {
				t.Errorf("GET %s: status %d, want a 404 that is not HTML (it has an extension)", p, resp.StatusCode)
			}
			continue
		}
		assertUIPage(t, "GET "+p, resp, body, index)
	}
}

func TestEncodedSlashAfterApiDoesNotTurnIntoTheUI(t *testing.T) {
	// "/api%2fnope" is either an API path (401 without a token) or an encoded
	// slash the UI rejects (400 or 404). It is never the UI page and never a
	// server error.
	srv := newServer(t)
	for _, p := range []string{"/api%2fnope", "/api%2Fv1%2Fsearch", "/%61pi/nope", "/api%2f"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, nil)
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusBadRequest, http.StatusNotFound:
		default:
			t.Errorf("GET %s without a token: status %d, want 400, 401 or 404", p, resp.StatusCode)
		}
		if bytes.Contains(body, uiIndex(t)) {
			t.Errorf("GET %s without a token: the UI page was served for an API-looking path", p)
		}
	}
}

// --- everything else is unchanged --------------------------------------------

func TestDocsAndSwaggerAreNotTheUI(t *testing.T) {
	srv := newServer(t)
	index := uiIndex(t)
	for _, p := range []string{"/openapi.yaml", "/openapi.json", "/swagger-ui", "/swagger-ui/", "/swagger-ui/swagger-ui.css", "/swagger-ui/nope", "/swagger-ui/LICENSE"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, nil)
		if resp.StatusCode == http.StatusOK || bytes.Contains(body, index) {
			t.Errorf("GET %s without a token: status %d; the docs stay behind the token and are not the UI", p, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Security-Policy"); got == uiCSP {
			t.Errorf("GET %s: carries the UI's CSP", p)
		}
		resp, body = uiDo(t, http.MethodGet, srv.URL+p, bearer(testToken))
		if bytes.Contains(body, index) {
			t.Errorf("GET %s with a token: the UI page was served", p)
		}
		if got := resp.Header.Get("Content-Security-Policy"); got == uiCSP {
			t.Errorf("GET %s with a token: carries the UI's CSP; the docs keep their own", p)
		}
	}
	resp, _ := uiDo(t, http.MethodGet, srv.URL+"/swagger-ui", bearer(testToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "style-src 'self' 'unsafe-inline'") {
		t.Errorf("/swagger-ui: status %d, CSP %q, want 200 with the Swagger UI's own policy", resp.StatusCode, resp.Header.Get("Content-Security-Policy"))
	}
	resp, body := uiDo(t, http.MethodGet, srv.URL+"/openapi.json", bearer(testToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "json") || !strings.Contains(string(body), `"openapi"`) {
		t.Errorf("/openapi.json: status %d, type %q: not the OpenAPI document", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestSwaggerPathsStayNotFoundWhenSwaggerIsOffNotTheUI(t *testing.T) {
	srv := newServerWith(t, platform.Config{SwaggerUIEnabled: false})
	for _, p := range []string{"/swagger-ui", "/swagger-ui/", "/swagger-ui/swagger-ui.css", "/swagger-ui/nope"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, bearer(testToken))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s with Swagger UI off: status %d, want 404 (not the UI)", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "not_found")
	}
}

func TestMCPAndAPIRoutesAreNotShadowedByTheUI(t *testing.T) {
	srv := newServer(t)
	resp, body := apiGet(t, srv.URL+"/api/v1/search?q=kubernetes")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		t.Errorf("search: status %d type %q: %.100s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != "" {
		t.Errorf("an API response carries a CSP %q; the UI's applies to UI paths only", got)
	}
	resp, body = uiDo(t, http.MethodPost, srv.URL+"/mcp", nil)
	assertUnauthorized(t, "POST /mcp without a token", resp, body)
}

// The admin listener never serves the UI, not at / and not for any UI path.
func TestAdminListenerNeverServesTheUI(t *testing.T) {
	cfg := publicConfig(t)
	pub, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	admin := adminServer(t, cfg, pub, platform.NewRegistry(0))
	index := uiIndex(t)
	for _, p := range []string{"/", "/index.html", "/projects", "/assets/x.js", "/favicon.ico", "/apix"} {
		resp, body := uiDo(t, http.MethodGet, "http://"+admin+p, nil)
		if resp.StatusCode == http.StatusOK || bytes.Contains(body, index) {
			t.Errorf("admin GET %s: status %d: the admin listener served the UI", p, resp.StatusCode)
		}
		if resp.Header.Get("Content-Security-Policy") == uiCSP {
			t.Errorf("admin GET %s carries the UI's CSP", p)
		}
	}
	for _, p := range []string{"/livez", "/readyz", "/version"} {
		if resp, _ := uiDo(t, http.MethodGet, "http://"+admin+p, nil); resp.StatusCode != http.StatusOK && p != "/readyz" {
			t.Errorf("admin GET %s: status %d, want 200 (unchanged)", p, resp.StatusCode)
		}
	}
}

// The UI is a public listener feature and the same handler serves it with
// Start: nothing about it depends on the poll loop.
func TestPublicHandlerServesTheUIOverARealListener(t *testing.T) {
	pub, err := PublicHandler(publicConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	resp, body := uiDo(t, http.MethodGet, srv.URL+"/", nil)
	assertUIPage(t, "GET /", resp, body, uiIndex(t))
}

// --- hostile input over the wire ---------------------------------------------

func TestHostilePathsOverTheWireNeverReadOutsideTheUI(t *testing.T) {
	srv := newServer(t)
	for _, raw := range []string{
		"/%2e%2e/%2e%2e/etc/passwd", "/..%2f..%2fetc/passwd", "/assets/..%2f..%2f..%2fgo.mod", "/%2e%2e/go.mod",
		"/assets%2f..%2f..%2fgo.mod", "/%5c..%5cgo.mod", "/assets/%00.js", "/%00", "/a%00b", "//go.mod", "/assets//x.js",
		"/" + strings.Repeat("a", 70000), "/" + strings.Repeat("%2e%2e/", 5000) + "go.mod",
	} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+raw, nil)
		show := raw
		if len(show) > 50 {
			show = show[:50] + "..."
		}
		s := string(body)
		for _, leak := range []string{"module github.com/thschue/goldie", "root:x:", "/home/", "goroutine", ".go:"} {
			if strings.Contains(s, leak) {
				t.Errorf("GET %s: the response contains %q", show, leak)
			}
		}
		if resp.StatusCode >= 500 {
			t.Errorf("GET %s: status %d, want no server error", show, resp.StatusCode)
		}
		if len(raw) > 8192 && (resp.StatusCode < 400 || resp.StatusCode >= 500) {
			t.Errorf("GET %s: status %d, want a 4xx for a very long path", show, resp.StatusCode)
		}
		if resp.StatusCode == http.StatusOK && !bytes.Equal(body, uiIndex(t)) {
			t.Errorf("GET %s: 200 with something other than the index page: %.100s", show, s)
		}
	}
}

// --- startup warning -----------------------------------------------------------

// logRecords are the JSON log lines of logs, parsed.
func logRecords(t *testing.T, logs *lockedBuffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range logs.lines() {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", l)
		}
		out = append(out, rec)
	}
	return out
}

func TestStartupWarnsOnceWhenTheUIIsNotBuilt(t *testing.T) {
	logs := &lockedBuffer{}
	cfg := publicConfig(t)
	if _, err := PublicHandler(cfg, platform.NewLogger(logs, slog.LevelDebug, "server")); err != nil {
		t.Fatal(err)
	}
	var warns []map[string]any
	for _, rec := range logRecords(t, logs) {
		if rec["msg"] == uiNotBuiltMessage {
			warns = append(warns, rec)
		}
	}
	if ui.Built() {
		if len(warns) != 0 {
			t.Errorf("the UI is built, but the server warned: %v", warns)
		}
		return
	}
	if len(warns) != 1 {
		t.Fatalf("got %d %q warnings at startup, want exactly 1; log:\n%s", len(warns), uiNotBuiltMessage, strings.Join(logs.lines(), "\n"))
	}
	if warns[0]["level"] != "warn" {
		t.Errorf("the UI-not-built line has level %v, want warn", warns[0]["level"])
	}
}

// --- PC-01: the probes are reserved on the public listener -------------------

// The public listener never serves /livez, /readyz or /version, and a monitor
// pointed at the wrong port must not read a 200: these three exact paths are
// not UI fallback paths. They answer the 404 envelope, JSON with no HTML, no
// CSP and no index page, whether or not the UI is built, with or without a
// token. Matching is exact and case-sensitive: "/livez/x", "/readyzz" and
// "/Version" are ordinary extensionless paths and fall back to the UI.
func TestProbePathsAreNotFoundOnThePublicListenerNotTheUI(t *testing.T) {
	srv := newServer(t)
	index := uiIndex(t)
	for _, p := range []string{"/livez", "/readyz", "/version"} {
		for who, auth := range map[string]func(*http.Request){"no token": nil, "token": bearer(testToken)} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				what := method + " " + p + " " + who
				resp, body := uiDo(t, method, srv.URL+p, auth)
				if resp.StatusCode != http.StatusNotFound {
					t.Errorf("%s: status %d, want 404", what, resp.StatusCode)
				}
				if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
					t.Errorf("%s: Content-Type %q, want application/json", what, resp.Header.Get("Content-Type"))
				}
				if resp.Header.Get("Content-Security-Policy") != "" {
					t.Errorf("%s: carries a CSP; it is not a UI path", what)
				}
				if bytes.Contains(body, index) || strings.Contains(strings.ToLower(string(body)), "<html") {
					t.Errorf("%s: the UI page was served", what)
				}
				if method == http.MethodGet {
					assertEnvelope(t, body, "not_found")
					if strings.Contains(string(body), strings.TrimPrefix(p, "/")) {
						t.Errorf("%s: the envelope echoes the path: %s", what, body)
					}
				}
			}
		}
	}
}

func TestProbeNearMissesFallBackToTheUI(t *testing.T) {
	srv := newServer(t)
	index := uiIndex(t)
	for _, p := range []string{"/livez/x", "/readyzz", "/Version", "/LIVEZ", "/readyz2", "/xversion", "/version/1"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, nil)
		assertUIPage(t, "GET "+p, resp, body, index)
	}
}

func TestAdminListenerStillServesTheRealProbes(t *testing.T) {
	cfg := publicConfig(t)
	pub, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := platform.NewRegistry(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startReturns(t, pub, ctx, reg)
	admin := adminServer(t, cfg, pub, reg)
	index := uiIndex(t)
	for _, p := range []string{"/livez", "/readyz", "/version"} {
		resp, body := uiDo(t, http.MethodGet, "http://"+admin+p, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("admin GET %s: status %d, want 200", p, resp.StatusCode)
		}
		if bytes.Contains(body, index) || strings.Contains(strings.ToLower(string(body)), "<html") {
			t.Errorf("admin GET %s: HTML on the admin listener", p)
		}
	}
}

func TestStartupWithoutALoggerStillStarts(t *testing.T) {
	// A nil logger discards; the warning must not panic on it.
	if _, err := PublicHandler(publicConfig(t), nil); err != nil {
		t.Fatalf("PublicHandler with a nil logger: %v", err)
	}
}

func TestTheWarningDoesNotRepeatPerRequest(t *testing.T) {
	logs := &lockedBuffer{}
	pub, err := PublicHandler(publicConfig(t), platform.NewLogger(logs, slog.LevelDebug, "server"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	before := len(logs.lines())
	for range 5 {
		uiDo(t, http.MethodGet, srv.URL+"/", nil)
	}
	if after := len(logs.lines()); after != before {
		t.Errorf("serving the UI wrote %d log lines; the warning is for startup only", after-before)
	}
}
