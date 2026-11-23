package platform

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The span view never carries an article id or a query string, however the
// path is spelled; other paths keep theirs.
func TestSpanViewRedactsTheArticleID(t *testing.T) {
	for target, want := range map[string]string{
		"/api/v1/articles/secret-id":            "/api/v1/articles/{id}",
		"/api/v1/articles/platform%2Fsecret":    "/api/v1/articles/{id}",
		"//api/v1/articles/secret-id":           "/api/v1/articles/{id}",
		"/api/v1/./articles/secret-id?x=secret": "/api/v1/articles/{id}",
		"/api/v1/search?q=secret":               "/api/v1/search",
		"/api/v1/projects":                      "/api/v1/projects",
	} {
		r := httptest.NewRequest("GET", target, nil)
		v := spanView(r, false)
		if got := v.URL.Path; got != want {
			t.Errorf("%s: span path %q, want %q", target, got, want)
		}
		if s := v.URL.String(); strings.Contains(s, "secret") {
			t.Errorf("%s: span URL %q carries the id or query", target, s)
		}
		if r.URL.String() != target {
			t.Errorf("%s: the handler's URL changed to %q", target, r.URL.String())
		}
	}
}
