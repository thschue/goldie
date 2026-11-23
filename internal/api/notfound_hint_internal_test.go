package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// Acceptance tests for #535: the not_found hints for a plain miss are
// client-neutral (no API operation named), so every client can show them as
// is. A Suppression keeps its own hint, which names the entry to remove. The
// skill and agent hints are the article hint with "skill" or "agent" for
// "article", word for word.

const (
	missArticleHint = "Search for the article, then use an id from its results."
	missSkillHint   = "Search for the skill, then use an id from its results."
	missAgentHint   = "Search for the agent, then use an id from its results."
	notFoundProject = "github.com/acme/shop"
)

func notFoundMux(t *testing.T) *http.ServeMux {
	t.Helper()
	art := []byte("---\nname: Style\nkeywords: [style]\n---\nKeep it small.\n")
	snap, err := engine.NewFSSource(fstest.MapFS{
		"global/articles/style.md":                         {Data: art},
		"global/articles/platform/notes.md":                {Data: art},
		"projects/github.com/acme/shop/project.yaml":       {Data: []byte("suppress: [article:platform/notes]\n")},
		"projects/github.com/acme/plain/articles/plain.md": {Data: art},
	}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(func() *engine.Snapshot { return snap })
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Register(mux)
	return mux
}

func notFound(t *testing.T, mux *http.ServeMux, target string) (message, hint string) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET %s: status %d, want 404: %s", target, rec.Code, rec.Body)
	}
	var env contract.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("GET %s: body %q is not the envelope: %v", target, rec.Body, err)
	}
	if env.Error.Code != "not_found" {
		t.Errorf("GET %s: code %q, want not_found", target, env.Error.Code)
	}
	return env.Error.Message, env.Error.Details.Hint
}

// noAPIOperation fails when a hint names an API operation or the transport.
func noAPIOperation(t *testing.T, target, hint string) {
	t.Helper()
	for _, bad := range []string{"operation", "/api", "GET ", "endpoint", "search_knowledge", "goldie "} {
		if strings.Contains(hint, bad) {
			t.Errorf("GET %s: hint %q names %q; a plain-miss hint must be client-neutral", target, hint, bad)
		}
	}
}

func TestUnknownArticleIDHintIsClientNeutral(t *testing.T) {
	mux := notFoundMux(t)
	for _, target := range []string{
		"/api/v1/articles/no-such-article",
		"/api/v1/articles/no%2Fsuch",
	} {
		msg, hint := notFound(t, mux, target)
		if msg != "no such article" {
			t.Errorf("GET %s: message %q, want %q", target, msg, "no such article")
		}
		if hint != missArticleHint {
			t.Errorf("GET %s: hint %q, want %q", target, hint, missArticleHint)
		}
		noAPIOperation(t, target, hint)
	}
}

func TestUnknownArticleIDWithAProjectInScopeGetsTheSameHint(t *testing.T) {
	mux := notFoundMux(t)
	for _, project := range []string{notFoundProject, "github.com/acme/plain", "github.com/acme/unregistered"} {
		target := "/api/v1/articles/no-such-article?project=" + project
		_, hint := notFound(t, mux, target)
		if hint != missArticleHint {
			t.Errorf("GET %s: hint %q, want %q", target, hint, missArticleHint)
		}
		noAPIOperation(t, target, hint)
	}
}

func TestSuppressedArticleKeepsItsSuppressionHint(t *testing.T) {
	mux := notFoundMux(t)
	target := "/api/v1/articles/platform%2Fnotes?project=" + notFoundProject
	_, hint := notFound(t, mux, target)
	for _, want := range []string{"article:platform/notes", "projects/github.com/acme/shop/project.yaml"} {
		if !strings.Contains(hint, want) {
			t.Errorf("GET %s: hint %q does not name %q", target, hint, want)
		}
	}
	if hint == missArticleHint {
		t.Errorf("GET %s: hint %q is the plain-miss hint, want the Suppression hint", target, hint)
	}
}

func TestUnknownSkillAndAgentHintsAreClientNeutral(t *testing.T) {
	mux := notFoundMux(t)
	for target, want := range map[string]string{
		"/api/v1/skills/no-such-skill": missSkillHint,
		"/api/v1/agents/no-such-agent": missAgentHint,
	} {
		_, hint := notFound(t, mux, target)
		if hint != want {
			t.Errorf("GET %s: hint %q, want %q", target, hint, want)
		}
		noAPIOperation(t, target, hint)
	}
}
