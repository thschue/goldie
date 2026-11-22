package app

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// rawGet is an authenticated GET without the spec check: the Swagger UI files
// are not API operations, so the spec does not describe their 200 responses,
// and they take the API's token (#101).
func rawGet(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer(testToken)(req)
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // test server URL
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

var swaggerPaths = []string{
	"/swagger-ui",
	"/swagger-ui/",
	"/swagger-ui/swagger-initializer.js",
	"/swagger-ui/swagger-ui-bundle.js",
	"/swagger-ui/swagger-ui.css",
}

func TestSwaggerUILoadsTheServedSpec(t *testing.T) {
	srv := newServer(t)
	resp, page := rawGet(t, srv.URL+"/swagger-ui")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("/swagger-ui: status %d, Content-Type %q, want 200 text/html", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// Every script and stylesheet the page loads is served by this server.
	refs := regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(string(page), -1)
	if len(refs) != 3 {
		t.Fatalf("page references %d assets, want 3: %s", len(refs), page)
	}
	for _, m := range refs {
		if !strings.HasPrefix(m[1], "/swagger-ui/") {
			t.Errorf("page loads %q, want a path under /swagger-ui/ (no CDN)", m[1])
		}
		if r, _ := rawGet(t, srv.URL+m[1]); r.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", m[1], r.StatusCode)
		}
	}
	// The initializer points Swagger UI at the served spec, and that URL works.
	_, initJS := rawGet(t, srv.URL+"/swagger-ui/swagger-initializer.js")
	m := regexp.MustCompile(`url:\s*"([^"]+)"`).FindStringSubmatch(string(initJS))
	if m == nil || m[1] != "/openapi.json" {
		t.Fatalf("initializer url = %v, want /openapi.json", m)
	}
	_, spec := apiGet(t, srv.URL+m[1])
	if !strings.Contains(string(spec), `"openapi"`) {
		t.Errorf("the spec URL does not serve the OpenAPI document")
	}
}

func TestSwaggerUIAssetsComeFromTheEmbed(t *testing.T) {
	srv := newServer(t)
	assets, err := contract.SwaggerUIAssets()
	if err != nil {
		t.Fatal(err)
	}
	embedded := map[string][]byte{}
	for _, a := range assets {
		embedded[a.Name] = a.Body
	}
	for path, name := range map[string]string{
		"/swagger-ui/swagger-ui-bundle.js":   "swagger-ui-bundle.js",
		"/swagger-ui/swagger-ui.css":         "swagger-ui.css",
		"/swagger-ui/swagger-initializer.js": "swagger-initializer.js",
		"/swagger-ui":                        "index.html",
	} {
		resp, body := rawGet(t, srv.URL+path)
		if string(body) != string(embedded[name]) {
			t.Errorf("%s is not the embedded %s", path, name)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing X-Content-Type-Options: nosniff", path)
		}
	}
	if n := len(embedded["swagger-ui-bundle.js"]); n < 100_000 {
		t.Errorf("vendored bundle is %d bytes, want the real Swagger UI", n)
	}
}

func TestSwaggerUIOnlyServesTheListedFiles(t *testing.T) {
	srv := newServer(t)
	for _, p := range []string{
		"/swagger-ui/LICENSE", "/swagger-ui/vendor/VENDORED.md", "/swagger-ui/vendor/swagger-ui.css",
		"/swagger-ui/swagger-ui-bundle.js.LICENSE.txt", "/swagger-ui/nope", "/swagger-ui/index.html",
	} {
		resp, body := apiGet(t, srv.URL+p)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "not_found")
	}
	resp, body := do(t, http.MethodPost, srv.URL+"/swagger-ui", bearer(testToken))
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /swagger-ui: status %d, want 405", resp.StatusCode)
	}
	assertEnvelope(t, body, "method_not_allowed")
}

func TestSwaggerUIDisabledIsNotFound(t *testing.T) {
	srv := newServerWith(t, Options{SwaggerUI: false})
	for _, p := range swaggerPaths {
		resp, body := apiGet(t, srv.URL+p)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 when disabled", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "not_found")
	}
	// The spec stays up: only the UI is switched off.
	if resp, _ := apiGet(t, srv.URL+"/openapi.json"); resp.StatusCode != http.StatusOK {
		t.Errorf("/openapi.json: status %d, want 200", resp.StatusCode)
	}
}

// Swagger UI must render the served spec and nothing a request supplies, and
// must not reach another host. The page itself is static, so the guard is in
// the initializer's configuration.
func TestSwaggerUIRendersOnlyTheServedSpec(t *testing.T) {
	srv := newServer(t)
	_, plain := rawGet(t, srv.URL+"/swagger-ui")
	_, queried := rawGet(t, srv.URL+"/swagger-ui?url=https://other.test/spec.json&configUrl=https://other.test/c.json")
	if string(plain) != string(queried) {
		t.Error("the page changes with the query string")
	}
	_, initJS := rawGet(t, srv.URL+"/swagger-ui/swagger-initializer.js")
	cfg := string(initJS)
	if !regexp.MustCompile(`queryConfigEnabled:\s*false`).MatchString(cfg) {
		t.Error("initializer must set queryConfigEnabled: false so the query string cannot configure Swagger UI")
	}
	for _, bad := range []string{"configUrl", "urls:", "StandaloneLayout", "location.search"} {
		if strings.Contains(cfg, bad) {
			t.Errorf("initializer contains %q: the spec must not be chosen by the caller", bad)
		}
	}
	external := regexp.MustCompile(`(?i)https?://`)
	for name, text := range map[string]string{"page": string(plain), "initializer": cfg} {
		if external.MatchString(text) {
			t.Errorf("%s names an external host: assets must be vendored, not fetched", name)
		}
	}
}

// The page takes bearer tokens, so it runs under a strict CSP: scripts and
// connections only from this origin, no eval, no framing, no <base> rewrite.
// Styles keep 'unsafe-inline' because Swagger UI sets inline styles.
func TestSwaggerUIIsServedWithAStrictCSP(t *testing.T) {
	srv := newServer(t)
	want := []string{
		"default-src 'none'", "script-src 'self'", "connect-src 'self'",
		"img-src 'self' data:", "base-uri 'none'", "frame-ancestors 'none'",
	}
	for _, p := range swaggerPaths {
		resp, _ := rawGet(t, srv.URL+p)
		csp := resp.Header.Get("Content-Security-Policy")
		for _, d := range want {
			if !strings.Contains(csp, d) {
				t.Errorf("%s: CSP %q lacks %q", p, csp, d)
			}
		}
		for _, bad := range []string{"unsafe-eval", "*", "http:", "https:"} {
			if strings.Contains(csp, bad) {
				t.Errorf("%s: CSP %q allows %q", p, csp, bad)
			}
		}
	}
}

// A wrong method on a Swagger UI path is the 405 envelope, still under the
// page's CSP: every response from those paths carries it.
func TestSwaggerUIMethodNotAllowedKeepsTheCSP(t *testing.T) {
	srv := newServer(t)
	for _, p := range swaggerPaths {
		resp, body := do(t, http.MethodPost, srv.URL+p, bearer(testToken))
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: status %d, want 405", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "method_not_allowed")
		if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
			t.Errorf("POST %s: Allow %q, want GET, HEAD", p, got)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); csp != swaggerUICSP {
			t.Errorf("POST %s: CSP %q, want the Swagger UI policy", p, csp)
		}
	}
}
