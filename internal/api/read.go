package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// maxQueryBytes bounds the search words, as the spec's maxLength on q says.
const maxQueryBytes = 1024

// Route is one API operation: its method and its path exactly as the spec
// writes it, which is also a net/http ServeMux pattern.
type Route struct {
	Method  string
	Path    string
	Handler http.Handler
}

// Routes returns the read operations over one Snapshot. They carry no auth:
// the caller wraps them, and the rest of /api/, in RequireToken.
func Routes(snap *engine.Snapshot) []Route {
	return []Route{
		{http.MethodGet, "/api/v1/search", getOnly(func(w http.ResponseWriter, r *http.Request) {
			search(snap, w, r)
		})},
		{http.MethodGet, "/api/v1/articles/{id}", getOnly(func(w http.ResponseWriter, r *http.Request) {
			article(snap, w, r)
		})},
	}
}

func search(snap *engine.Snapshot, w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()["q"]
	if len(qs) != 1 || strings.TrimSpace(qs[0]) == "" || len(qs[0]) > maxQueryBytes {
		WriteError(w, http.StatusBadRequest, "bad_request", "missing or invalid search words",
			"Pass one q parameter with 1 to 1024 bytes of search words.")
		return
	}
	res := contract.SearchResults{Query: qs[0], Results: []contract.SearchHit{}}
	for _, h := range snap.Search(qs[0]) {
		res.Results = append(res.Results, contract.SearchHit{
			Id:       h.ID,
			Name:     h.Name,
			Keywords: list(h.Keywords),
			Score:    h.Score,
			Matched:  list(h.Matched),
		})
	}
	writeJSON(w, http.StatusOK, res)
}

func article(snap *engine.Snapshot, w http.ResponseWriter, r *http.Request) {
	a, ok := snap.Article(r.PathValue("id"))
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "no such article",
			"Search for the article with the search operation, then use an id from its results.")
		return
	}
	writeJSON(w, http.StatusOK, contract.Article{
		Id:       a.ID,
		Name:     a.Name,
		Keywords: list(a.Keywords),
		Content:  a.Content,
	})
}

// list makes a nil slice an empty one, so it encodes as [] as the spec requires.
func list(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// getOnly answers GET and HEAD with h and rejects other methods.
func getOnly(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed",
				"Use a method listed in the Allow header.")
			return
		}
		h(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes the error envelope {"error":{code,message,details}},
// built from the generated contract types (ADR 0014). hint is the next-step
// hint in details; it must not echo the request or contain addresses, paths
// or stack traces.
func WriteError(w http.ResponseWriter, status int, code, message, hint string) {
	writeJSON(w, status, contract.ErrorEnvelope{Error: contract.Error{
		Code:    code,
		Message: message,
		Details: contract.ErrorDetails{Hint: hint},
	}})
}
