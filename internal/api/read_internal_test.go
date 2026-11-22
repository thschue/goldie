package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env contract.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("body %q is not the envelope: %v", rec.Body.String(), err)
	}
	return env.Error.Message
}

// The search-words message belongs to the search operation: a bad q on any
// other operation is the generic parameter envelope.
func TestRequestErrorScopesSearchWordsToSearch(t *testing.T) {
	for pattern, want := range map[string]string{
		searchPattern:                     badSearch.Error.Message,
		http.MethodGet + " /api/v1/other": "invalid request parameters",
		"":                                "invalid request parameters",
	} {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.Pattern = pattern
		rec := httptest.NewRecorder()
		requestError(rec, r, &contract.RequiredParamError{ParamName: "q"})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", pattern, rec.Code)
		}
		if got := errorMessage(t, rec); got != want {
			t.Errorf("%q: message %q, want %q", pattern, got, want)
		}
	}
}

// The generated router registers search under searchPattern, so a missing or
// repeated q there gets the search-words message.
func TestRegisteredSearchGetsTheSearchWordsMessage(t *testing.T) {
	srv, err := NewServer(func() *engine.Snapshot { return &engine.Snapshot{} })
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Register(mux)
	for _, target := range []string{"/api/v1/search", "/api/v1/search?q=a&q=b"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if got := errorMessage(t, rec); got != badSearch.Error.Message {
			t.Errorf("%s: message %q, want %q", target, got, badSearch.Error.Message)
		}
	}
}
