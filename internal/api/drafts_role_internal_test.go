package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// A Server mounted without a Require guard has no Role in the request
// context, and then drafts=true is refused: drafts are shown only to a caller
// proven to be an operator (#273).
func TestDraftsWithoutARoleAreRefused(t *testing.T) {
	s, err := NewServer(func() *engine.Snapshot { return nil })
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	search, _ := s.SearchArticles(context.Background(), contract.SearchArticlesRequestObject{
		Params: contract.SearchArticlesParams{Q: "a", Drafts: &yes}})
	if _, ok := search.(contract.SearchArticles403JSONResponse); !ok {
		t.Errorf("search with drafts=true and no Role answered %T, want the 403", search)
	}
	lookup, _ := s.GetArticle(context.Background(), contract.GetArticleRequestObject{
		Id: "a", Params: contract.GetArticleParams{Drafts: &yes}})
	if _, ok := lookup.(contract.GetArticle403JSONResponse); !ok {
		t.Errorf("lookup with drafts=true and no Role answered %T, want the 403", lookup)
	}
}

// Require puts the token's Role in the context next sees, beside its name.
func TestRequireSetsTheTokenRole(t *testing.T) {
	tokens, err := ParseTokens("ci:reader:" + "r0123456789abcdef0123456789abcdef" + "\nops:operator:" + "o0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	for secret, want := range map[string]Role{
		"r0123456789abcdef0123456789abcdef": RoleReader,
		"o0123456789abcdef0123456789abcdef": RoleOperator,
	} {
		var got Role
		var ok bool
		h := tokens.Require(RoleReader, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got, ok = TokenRole(r.Context())
		}))
		r := httptest.NewRequest(http.MethodGet, "/api/v1/search", nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		h.ServeHTTP(httptest.NewRecorder(), r)
		if !ok || got != want {
			t.Errorf("TokenRole = %v, %v; want %v", got, ok, want)
		}
	}
	if _, ok := TokenRole(context.Background()); ok {
		t.Error("TokenRole reports a Role without a Require guard")
	}
}
