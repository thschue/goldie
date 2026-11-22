package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// testToken is the API token of the test server; testContent is its dir Source.
const (
	testToken   = "test-token-0123456789abcdef0123456789"
	testContent = "testdata/content"
)

func bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// apiGet is an authenticated GET; every response is validated against the spec.
func apiGet(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	return do(t, http.MethodGet, url, bearer(testToken))
}

// errorHint returns the code and details.hint of an error envelope.
func errorHint(t *testing.T, body []byte) (code, hint string) {
	t.Helper()
	assertEnvelope(t, body, "")
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Hint string `json:"hint"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Error.Code, env.Error.Details.Hint
}

func TestAPIRejectsMissingOrWrongToken(t *testing.T) {
	srv := newServer(t)
	paths := []string{
		"/api/v1/search?q=kubernetes",
		"/api/v1/articles/agent-loops",
		"/api/v1/articles/platform%2Fdeploy",
		"/api/v1/nope",
		"/api/",
		"/api",
	}
	const wrong = "wrong-token-presented-here"
	auths := map[string]func(*http.Request){
		"no header":          nil,
		"wrong token":        bearer(wrong),
		"token prefix":       bearer(testToken[:len(testToken)-1]),
		"token suffix":       bearer(testToken + "x"),
		"empty bearer":       func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") },
		"scheme only":        func(r *http.Request) { r.Header.Set("Authorization", "Bearer") },
		"basic scheme":       func(r *http.Request) { r.Header.Set("Authorization", "Basic "+testToken) },
		"token without type": func(r *http.Request) { r.Header.Set("Authorization", testToken) },
		"token in query":     func(r *http.Request) { r.URL.RawQuery += "&access_token=" + testToken },
	}
	for _, p := range paths {
		for name, auth := range auths {
			resp, body := do(t, http.MethodGet, srv.URL+p, auth)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s, %s: status %d, want 401", p, name, resp.StatusCode)
				continue
			}
			if code, hint := errorHint(t, body); code != "unauthorized" || !strings.Contains(hint, "Bearer") {
				t.Errorf("%s, %s: code %q hint %q, want unauthorized with a bearer hint", p, name, code, hint)
			}
			if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("%s, %s: WWW-Authenticate %q, want Bearer", p, name, got)
			}
			for _, secret := range []string{testToken, wrong, testToken[:len(testToken)-1]} {
				if strings.Contains(string(body), secret) {
					t.Errorf("%s, %s: the body echoes a token: %s", p, name, body)
				}
			}
		}
	}
}

func TestAPIAcceptsTheTokenWithAnyCaseScheme(t *testing.T) {
	srv := newServer(t)
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		resp, _ := do(t, http.MethodGet, srv.URL+"/api/v1/articles/agent-loops", func(r *http.Request) {
			r.Header.Set("Authorization", scheme+" "+testToken)
		})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("scheme %q: status %d, want 200", scheme, resp.StatusCode)
		}
	}
}

func TestSpecAndSwaggerUIStayOpenUntilSignIn(t *testing.T) {
	// #48's decision puts /openapi.* and /swagger-ui under the auth of #11,
	// not under this token, which protects /api/ only.
	srv := newServer(t)
	for _, p := range append([]string{"/openapi.json", "/openapi.yaml"}, swaggerPaths...) {
		if resp, _ := rawGet(t, srv.URL+p); resp.StatusCode != http.StatusOK {
			t.Errorf("%s without a token: status %d, want 200", p, resp.StatusCode)
		}
	}
}

type searchResults struct {
	Query   string `json:"query"`
	Results []struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Keywords []string `json:"keywords"`
		Score    int      `json:"score"`
		Matched  []string `json:"matched"`
	} `json:"results"`
}

func TestSearchReturnsScoredHitsBestFirst(t *testing.T) {
	srv := newServer(t)
	resp, body := apiGet(t, srv.URL+"/api/v1/search?q=Kubernetes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got searchResults
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Query != "Kubernetes" {
		t.Errorf("query = %q", got.Query)
	}
	// deploy: exact keyword (4) + name (2); notes: name only (2).
	if len(got.Results) != 2 {
		t.Fatalf("results = %+v, want 2 hits", got.Results)
	}
	first, second := got.Results[0], got.Results[1]
	if first.ID != "platform/deploy" || first.Score != 6 || first.Name != "Deploying to Kubernetes" ||
		strings.Join(first.Matched, ",") != "kubernetes,name" || strings.Join(first.Keywords, ",") != "kubernetes,deploy,helm" {
		t.Errorf("first hit = %+v", first)
	}
	if second.ID != "platform/notes" || second.Score != 2 || second.Keywords == nil {
		t.Errorf("second hit = %+v (keywords must be an empty list, not null)", second)
	}
}

// Every search result carries kind and scope (ADR 0005).
func TestSearchHitsCarryKindAndScope(t *testing.T) {
	srv := newServer(t)
	resp, body := apiGet(t, srv.URL+"/api/v1/search?q=kubernetes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		Results []struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Scope string `json:"scope"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Results) == 0 {
		t.Fatalf("no hits: %s", body)
	}
	for _, h := range got.Results {
		if h.Kind != "article" || h.Scope != "global" {
			t.Errorf("hit %s: kind %q, scope %q, want article and global", h.ID, h.Kind, h.Scope)
		}
	}
}

func TestSearchWithoutHitsIsAnEmptyList(t *testing.T) {
	srv := newServer(t)
	resp, body := apiGet(t, srv.URL+"/api/v1/search?q=cobol")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"results":[]`) {
		t.Fatalf("status %d body %s, want 200 with an empty results list", resp.StatusCode, body)
	}
}

func TestSearchRejectsMissingOrOversizedQuery(t *testing.T) {
	srv := newServer(t)
	for name, query := range map[string]string{
		"missing":   "",
		"empty":     "?q=",
		"blank":     "?q=%20%20",
		"oversized": "?q=" + strings.Repeat("a", 1025),
		"repeated":  "?q=a&q=b",
	} {
		resp, body := apiGet(t, srv.URL+"/api/v1/search"+query)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, resp.StatusCode)
			continue
		}
		if code, hint := errorHint(t, body); code != "bad_request" || !strings.Contains(hint, "q") {
			t.Errorf("%s: code %q hint %q", name, code, hint)
		}
		if strings.Contains(string(body), "aaaa") {
			t.Errorf("%s: the body echoes the query", name)
		}
	}
	if resp, _ := apiGet(t, srv.URL+"/api/v1/search?q="+strings.Repeat("a", 1024)); resp.StatusCode != http.StatusOK {
		t.Errorf("1024-byte query: status %d, want 200", resp.StatusCode)
	}
}

func TestArticleLookup(t *testing.T) {
	srv := newServer(t)
	for path, wantID := range map[string]string{
		"/api/v1/articles/agent-loops":        "agent-loops",
		"/api/v1/articles/AGENT-LOOPS":        "agent-loops",
		"/api/v1/articles/platform%2Fdeploy":  "platform/deploy", // ADR 0015: nested ids keep their "/"
		"/api/v1/articles/Platform%2FDeploy":  "platform/deploy",
		"/api/v1/articles/platform%2fnotes":   "platform/notes",
		"/api/v1/articles/agent%2Dloops":      "agent-loops",
		"/api/v1/articles/platform%2Fdeploy/": "",
		"/api/v1/articles/platform/deploy":    "", // ADR 0016: a bare "/" is not a lookup
	} {
		resp, body := apiGet(t, srv.URL+path)
		if wantID == "" {
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d: %s", path, resp.StatusCode, body)
			continue
		}
		var a struct {
			ID, Name, Content string
			Keywords          []string
		}
		if err := json.Unmarshal(body, &a); err != nil {
			t.Fatal(err)
		}
		if a.ID != wantID || a.Name == "" || a.Content == "" || a.Keywords == nil {
			t.Errorf("%s: article = %+v, want id %q with name, content and a keyword list", path, a, wantID)
		}
	}
}

func TestArticleContentIsServedAsJSONData(t *testing.T) {
	srv := newServer(t)
	resp, body := apiGet(t, srv.URL+"/api/v1/articles/platform%2Fdeploy")
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type %q", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
	if strings.Contains(string(body), "<script>") {
		t.Errorf("markup is not escaped in the JSON body: %s", body)
	}
}

func TestArticleMissIs404WithASearchHint(t *testing.T) {
	srv := newServer(t)
	for _, id := range []string{
		"nope",
		"platform%2Fnope",
		"..%2F..%2Fetc%2Fpasswd",
		"%2Fetc%2Fpasswd",
		"global%2Farticles%2Fagent-loops",
		"agent-loops.md",
		"a%00b",
		"a%0Ab",
		"%3Cscript%3E",
		strings.Repeat("x", 4096),
	} {
		resp, body := apiGet(t, srv.URL+"/api/v1/articles/"+id)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%.40s: status %d, want 404", id, resp.StatusCode)
			continue
		}
		code, hint := errorHint(t, body)
		if code != "not_found" || !strings.Contains(strings.ToLower(hint), "search") {
			t.Errorf("%.40s: code %q hint %q, want not_found with a search hint", id, code, hint)
		}
		for name, re := range leaks {
			if m := re.FindString(string(body)); m != "" {
				t.Errorf("%.40s: error body contains a %s (%q): %s", id, name, m, body)
			}
		}
		for _, echo := range []string{"passwd", "script", "nope", "xxxx", "agent-loops"} {
			if strings.Contains(string(body), echo) {
				t.Errorf("%.40s: error body echoes the id: %s", id, body)
			}
		}
	}
}

func TestAPIOnlyAnswersGet(t *testing.T) {
	srv := newServer(t)
	for _, p := range []string{"/api/v1/search?q=x", "/api/v1/articles/agent-loops"} {
		resp, body := do(t, http.MethodPost, srv.URL+p, bearer(testToken))
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: status %d, want 405", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "method_not_allowed")
	}
}

func TestPublicHandlerFailsFastOnBadSettings(t *testing.T) {
	const marker = "s3cret marker 0123456789abcdef0123" // long enough that the space is what fails
	broken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(broken, "global", "articles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "global", "articles", "bad.md"), []byte("no frontmatter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing-dir")
	for name, tc := range map[string]struct {
		opts    Options
		varName string
		hidden  string // must not appear in the error
	}{
		"no token":           {Options{ContentDir: testContent}, platform.EnvAPIToken, ""},
		"token with a space": {Options{ContentDir: testContent, APIToken: marker}, platform.EnvAPIToken, marker},
		"token with a tab":   {Options{ContentDir: testContent, APIToken: "abc\tdef0123456789abcdef0123456789"}, platform.EnvAPIToken, "abc"},
		"token too short":    {Options{ContentDir: testContent, APIToken: testToken[:31]}, platform.EnvAPIToken, testToken[:31]},
		"no content dir":     {Options{APIToken: testToken}, platform.EnvContentDir, ""},
		"missing content":    {Options{APIToken: testToken, ContentDir: missing}, platform.EnvContentDir, missing},
		"invalid article":    {Options{APIToken: testToken, ContentDir: broken}, platform.EnvContentDir, broken},
	} {
		_, err := PublicHandler(tc.opts)
		if err == nil || !strings.Contains(err.Error(), tc.varName) {
			t.Errorf("%s: err = %v, want it to name %s", name, err, tc.varName)
			continue
		}
		var cfgErr *ConfigError
		if !errors.As(err, &cfgErr) {
			t.Errorf("%s: err = %v, want a *ConfigError so the caller can label it", name, err)
		}
		if tc.hidden != "" && strings.Contains(err.Error(), tc.hidden) {
			t.Errorf("%s: error echoes a value: %v", name, err)
		}
	}
}
