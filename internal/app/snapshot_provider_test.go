package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// Issue #95: the public API reads the current Snapshot on every request. The
// handlers take a provider, func() *engine.Snapshot, whose nil result means
// "no successful load yet". The router constructors newPublicMux,
// newPublicMuxKeepalive and publicRoutes, and api.NewServer, take that provider
// instead of a *engine.Snapshot. Existing callers were adapted with
// staticSnapshot and nothing else changed.

// staticSnapshot is the provider of one Snapshot fixed for good.
func staticSnapshot(s *engine.Snapshot) func() *engine.Snapshot {
	return func() *engine.Snapshot { return s }
}

// providerServer serves the public router over provider, as the real one is.
func providerServer(t *testing.T, provider func() *engine.Snapshot) *httptest.Server {
	t.Helper()
	tokens, err := api.OperatorToken(testToken)
	if err != nil {
		t.Fatal(err)
	}
	m, err := newPublicMuxKeepalive(provider, tokens, rateLimiter(platform.DefaultRateLimit()), nil, api.DefaultActivityKeepalive)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return srv
}

// writeArticle writes one global article of a dir Source.
func writeArticle(t *testing.T, root, id, name, body string) {
	t.Helper()
	p := filepath.Join(root, "global", "articles", id+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\nname: "+name+"\nkeywords: [swapword]\n---\n"+body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readOperationPaths are the concrete paths of every /api/v1 GET operation,
// found from the spec so a new read is covered without editing this test.
func readOperationPaths(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, o := range specOperations(t) {
		method, path, _ := strings.Cut(o, " ")
		if method != "GET" || !strings.HasPrefix(path, "/api/v1/") {
			continue
		}
		if path == "/api/v1/session/providers" {
			continue // anonymous and needs no Snapshot: it reads the configuration only (#645)
		}
		if strings.Contains(path, "{") {
			c, err := contracttest.ExamplePath(method, path)
			if err != nil {
				t.Fatal(err)
			}
			path = c
		}
		if path == "/api/v1/search" {
			path += "?q=a" // a valid read: bad input is a 400, tested apart
		}
		if path == "/api/v1/instructions" {
			path += "?target=claude" // a valid read: a bad target is a 400, tested apart
		}
		if path == "/api/v1/bundle" {
			path += "?target=claude" // a valid read: a bad target is a 400, tested apart
		}
		out = append(out, path)
	}
	if len(out) < 3 {
		t.Fatalf("found read operations %v, want at least search, article and projects", out)
	}
	return out
}

// Criterion: before the first successful load every /api/v1 read answers 503
// not_ready with an ADR 0014 hint. do validates each response against the
// spec, so this also fails until openapi.yaml declares the 503.
func TestReadsAnswerNotReadyBeforeTheFirstLoad(t *testing.T) {
	srv := providerServer(t, func() *engine.Snapshot { return nil })
	for _, path := range readOperationPaths(t) {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			resp, body := do(t, method, srv.URL+path, bearer(testToken))
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("%s %s: status %d, want 503", method, path, resp.StatusCode)
				continue
			}
			if method == http.MethodHead {
				continue
			}
			code, hint := errorHint(t, body)
			if code != "not_ready" {
				t.Errorf("%s: code %q, want not_ready", path, code)
			}
			if !strings.Contains(strings.ToLower(hint), "retry") {
				t.Errorf("%s: hint %q, want a retry-shortly next step", path, hint)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("%s: Content-Type %q, want JSON", path, ct)
			}
		}
	}
}

// The rejected alternative: never a 200 with an empty result, which a direct
// caller would read as "no matches".
func TestNotReadyIsNeverAnEmptySuccess(t *testing.T) {
	srv := providerServer(t, func() *engine.Snapshot { return nil })
	resp, body := apiGet(t, srv.URL+"/api/v1/search?q=kubernetes")
	if resp.StatusCode == http.StatusOK || strings.Contains(string(body), `"results"`) {
		t.Errorf("status %d body %s, want the not_ready error, not an empty result", resp.StatusCode, body)
	}
}

// The not_ready envelope is an ADR 0014 hint: it never echoes the request or
// carries addresses, paths or stack traces.
func TestNotReadyEnvelopeDoesNotLeak(t *testing.T) {
	srv := providerServer(t, func() *engine.Snapshot { return nil })
	host := strings.TrimPrefix(srv.URL, "http://")
	hostile := func(r *http.Request) {
		bearer(testToken)(r)
		r.Host = "internal.example.test:8443"
	}
	for _, path := range []string{
		"/api/v1/search?q=%2Fetc%2Fpasswd",
		"/api/v1/articles/..%2F..%2Fetc%2Fpasswd",
		"/api/v1/projects",
	} {
		resp, body := do(t, http.MethodGet, srv.URL+path, hostile)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", path, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "not_ready")
		for name, re := range leaks {
			if m := re.FindString(string(body)); m != "" {
				t.Errorf("%s: body contains a %s (%q): %s", path, name, m, body)
			}
		}
		for _, echo := range []string{host, "internal.example", "passwd", "etc"} {
			if strings.Contains(string(body), echo) {
				t.Errorf("%s: body echoes the request (%q): %s", path, echo, body)
			}
		}
	}
}

// Before the first load the token still comes first: an anonymous caller
// learns a 401, not that content is loading. Wrong methods keep their 405,
// bad input keeps its 400, and the static documents need no Snapshot.
func TestNotReadyDoesNotChangeAuthRoutingOrDocuments(t *testing.T) {
	srv := providerServer(t, func() *engine.Snapshot { return nil })
	for _, path := range readOperationPaths(t) {
		resp, body := do(t, http.MethodGet, srv.URL+path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a token: status %d, want 401", path, resp.StatusCode)
		} else {
			assertEnvelope(t, body, "unauthorized")
		}
	}
	resp, body := do(t, http.MethodPost, srv.URL+"/api/v1/search?q=a", bearer(testToken))
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", resp.StatusCode)
	} else {
		assertEnvelope(t, body, "method_not_allowed")
	}
	for _, doc := range []string{"/openapi.yaml", "/openapi.json"} {
		if resp, _ := do(t, http.MethodGet, srv.URL+doc, bearer(testToken)); resp.StatusCode != http.StatusOK {
			t.Errorf("%s before the first load: status %d, want 200", doc, resp.StatusCode)
		}
	}
	resp, body = apiGet(t, srv.URL+"/api/v1/search")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("search without q before the load: status %d, want 400: %s", resp.StatusCode, body)
	}
}

// Criterion: the spec declares the 503 on every read operation, so the
// response is additive and clients can read it.
func TestSpecDeclaresNotReadyOnEveryRead(t *testing.T) {
	paths, _ := specDoc(t)["paths"].(map[string]any)
	for path, item := range paths {
		if !strings.HasPrefix(path, "/api/v1/") {
			continue
		}
		if path == "/api/v1/session/providers" {
			continue // anonymous and needs no Snapshot (#645)
		}
		get, _ := item.(map[string]any)["get"].(map[string]any)
		responses, _ := get["responses"].(map[string]any)
		if _, ok := responses["503"]; !ok {
			t.Errorf("GET %s declares no 503 response", path)
		}
	}
}

// Criterion: a test swaps the Snapshot and sees the new content on the next
// request, with no restart; also from nil (not ready) to loaded.
func TestSwappingTheSnapshotShowsOnTheNextRequest(t *testing.T) {
	load := func(dir string) *engine.Snapshot {
		s, err := engine.NewDirSource(dir).Load(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first, second := t.TempDir(), t.TempDir()
	writeArticle(t, first, "alpha", "Alpha article", "first body")
	writeArticle(t, second, "beta", "Beta article", "second body")

	var current atomic.Pointer[engine.Snapshot]
	srv := providerServer(t, current.Load)

	if resp, _ := apiGet(t, srv.URL+"/api/v1/articles/alpha"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("before any load: status %d, want 503", resp.StatusCode)
	}
	current.Store(load(first))
	if resp, body := apiGet(t, srv.URL+"/api/v1/articles/alpha"); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "first body") {
		t.Fatalf("after the first load: status %d: %s", resp.StatusCode, body)
	}
	if resp, _ := apiGet(t, srv.URL+"/api/v1/articles/beta"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("beta before the swap: status %d, want 404", resp.StatusCode)
	}

	current.Store(load(second))
	resp, body := apiGet(t, srv.URL+"/api/v1/articles/beta")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "second body") {
		t.Fatalf("beta after the swap: status %d: %s", resp.StatusCode, body)
	}
	if resp, _ := apiGet(t, srv.URL+"/api/v1/articles/alpha"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("alpha after the swap: status %d, want 404 (the old Snapshot is gone)", resp.StatusCode)
	}
	_, body = apiGet(t, srv.URL+"/api/v1/search?q=swapword")
	if !regexp.MustCompile(`"id":\s*"beta"`).Match(body) || strings.Contains(string(body), `"alpha"`) {
		t.Errorf("search after the swap: %s, want beta only", body)
	}
}

// Criterion: the dir Source goes through the syncer, so one code path serves
// both. A Syncer over a dir Source is 503 until its first Poll, serves the
// directory after it, shows an edit on the next request after the next Poll,
// and keeps serving the last good content when a poll fails.
func TestDirSourceThroughTheSyncerServesAndRefreshes(t *testing.T) {
	dir := t.TempDir()
	writeArticle(t, dir, "alpha", "Alpha article", "first body")
	syncer := engine.NewSyncer(engine.NewDirSource(dir))
	srv := providerServer(t, syncer.Current)

	if resp, body := apiGet(t, srv.URL+"/api/v1/articles/alpha"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("before the first Poll: status %d, want 503: %s", resp.StatusCode, body)
	}
	if err := syncer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if resp, body := apiGet(t, srv.URL+"/api/v1/articles/alpha"); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "first body") {
		t.Fatalf("after the first Poll: status %d: %s", resp.StatusCode, body)
	}

	writeArticle(t, dir, "alpha", "Alpha article", "edited body")
	if _, body := apiGet(t, srv.URL+"/api/v1/articles/alpha"); !strings.Contains(string(body), "first body") {
		t.Errorf("an edit shows before the next Poll: %s", body)
	}
	if err := syncer.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, body := apiGet(t, srv.URL+"/api/v1/articles/alpha"); !strings.Contains(string(body), "edited body") {
		t.Errorf("after the next Poll: %s, want the edit", body)
	}

	// A failed poll never replaces the last good Snapshot and never turns the
	// API back to not_ready.
	broken := filepath.Join(dir, "global", "articles", "alpha.md")
	if err := os.WriteFile(broken, []byte("---\nname: [broken\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncer.Poll(t.Context()); err == nil {
		t.Fatal("poll of broken content succeeded")
	}
	if resp, body := apiGet(t, srv.URL+"/api/v1/articles/alpha"); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "edited body") {
		t.Errorf("after a failed Poll: status %d: %s, want the last good content", resp.StatusCode, body)
	}
}

// The provider is read per request, not once at construction: the route table
// built before any load registers every operation.
func TestRouteTableDoesNotNeedASnapshot(t *testing.T) {
	routes, err := publicRoutes(func() *engine.Snapshot { return nil }, testToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) == 0 {
		t.Error("no routes registered over a provider with no Snapshot yet")
	}
}
