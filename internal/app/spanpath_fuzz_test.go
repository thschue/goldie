package app

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// A request path is hostile input. SpanPath and ClientSpanPath never panic,
// and what they return is one of the contract's templates or the fixed
// placeholder, never a part of the input (#327).
func FuzzSpanPath(f *testing.F) {
	for _, s := range []string{
		"/api/v1/articles/x", "/API/V1/ARTICLES/x", "/api/v1/articles/a%2Fb", "/api/v1/articles/..%2Fx",
		"/api/v1/articles/../x", "/api/v1/articles/./x", "/api/v1/articles/%2e%2e/x", "/api/v1/articles/%2E%2E",
		"//api/v1/articles/x", "/api/v1/articles//x", "/api/v1/articles/x/", "/api/v1/articles/",
		"/prefix/api/v1/search", "/api/v1/articles/%zz", "/api/v1/articles/%", "/api/v1/articles/İ",
		"/api/v1/articles/K", "/ſearch", "/api/v1/articles/" + strings.Repeat("a", 3000),
		"", "/", "x", "/openapi.yaml", "/OPENAPI.JSON",
	} {
		f.Add(s)
	}
	templates, err := contract.RouteTemplates()
	if err != nil {
		f.Fatal(err)
	}
	allowed := map[string]bool{spanPathPlaceholder: true}
	for _, tmpl := range templates {
		allowed[tmpl] = true
	}
	f.Fuzz(func(t *testing.T, p string) {
		for name, fn := range map[string]func(string) string{"SpanPath": SpanPath, "ClientSpanPath": ClientSpanPath} {
			if got := fn(p); !allowed[got] {
				t.Errorf("%s(%.80q) = %.80q: not a contract template or the placeholder", name, p, got)
			}
		}
	})
}

// ClientSpanPath finds the route after the base URL's prefix, and a prefix
// never makes an id or a dot segment into a route.
func TestClientSpanPathFindsTheRouteAfterAPrefix(t *testing.T) {
	const article = "/api/v1/articles/{id}"
	for _, tc := range []struct{ name, in, want string }{
		{"no prefix", "/api/v1/articles/x", article},
		{"one segment", "/goldie/api/v1/articles/x", article},
		{"several segments", "/a/b/c/api/v1/articles/x", article},
		{"wrong case", "/Goldie/API/V1/Articles/x", article},
		{"encoded slash in the id", "/goldie/api/v1/articles/a%2Fb", article},
		{"id-like prefix", "/id-1234/api/v1/search", "/api/v1/search"},
		{"dot segment in the prefix", "/a/../api/v1/articles/x", spanPathPlaceholder},
		{"dot segment after the route", "/goldie/api/v1/articles/x/..", spanPathPlaceholder},
		{"encoded dot segment", "/goldie/%2e%2e/api/v1/articles/x", spanPathPlaceholder},
		{"no matching tail", "/goldie/other/path", spanPathPlaceholder},
		{"route with extra tail", "/goldie/api/v1/articles/a/b", spanPathPlaceholder},
		{"empty", "", spanPathPlaceholder},
		{"bad escape", "/goldie/api/v1/articles/%zz", spanPathPlaceholder},
		{"too long", "/api/v1/articles/" + strings.Repeat("a", maxSpanPath), spanPathPlaceholder},
	} {
		if got := ClientSpanPath(tc.in); got != tc.want {
			t.Errorf("%s: ClientSpanPath(%.60q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
