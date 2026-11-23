package cli_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #533: lookup gives the concrete search command for a
// plain miss in every Scope, and passes the server's other hints, a
// Suppression's, through unchanged. The server's plain-miss hint is the
// client-neutral one the API sends since #535.

const (
	plainMissHint   = "Search for the article, then use an id from its results."
	lookupMissHint  = "Search for the article with: goldie search <words>, then use an id from its results."
	suppressionHint = "The Project github.com/acme/shop suppresses security-baseline in projects/github.com/acme/shop/suppress.yaml. Remove that entry to read the Item, or look it up without the project."
	lookupTestProj  = "github.com/acme/shop"
)

// missServer answers every lookup 404 with hint, as the API does.
func missServer(t *testing.T, hint string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such article","details":{"hint":"` + hint + `"}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// lookupRun runs args with the test token.
func lookupRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runWithEnv(t, map[string]string{platform.EnvToken: localTestToken}, nil, args...)
}

func TestCLILookupOfAnUnknownIDNamesTheSearchCommandInEveryScope(t *testing.T) {
	srv := missServer(t, plainMissHint)
	cases := map[string][]string{
		"without a Project": {"lookup", "no-such-article", "--addr", srv.URL},
		"inside a Project":  {"lookup", "no-such-article", "--addr", srv.URL, "--project", lookupTestProj},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := lookupRun(t, args...)
			if err == nil {
				t.Fatal("lookup of an unknown id must fail")
			}
			if !strings.Contains(err.Error(), "Hint: "+lookupMissHint) {
				t.Errorf("lookup error = %q, want the hint %q", err, lookupMissHint)
			}
		})
	}
}

func TestCLILookupOfASuppressedIDPassesTheServersHintThrough(t *testing.T) {
	srv := missServer(t, suppressionHint)
	_, err := lookupRun(t, "lookup", "security-baseline", "--addr", srv.URL, "--project", lookupTestProj)
	if err == nil {
		t.Fatal("lookup of a suppressed id must fail")
	}
	if !strings.Contains(err.Error(), "Hint: "+suppressionHint) {
		t.Errorf("lookup error = %q, want the server's Suppression hint unchanged", err)
	}
	if strings.Contains(err.Error(), "goldie search") {
		t.Errorf("lookup error = %q, must not replace the Suppression hint with the search command", err)
	}
}
