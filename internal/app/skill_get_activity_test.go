package app

import (
	"net/http"
	"testing"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #412 at the HTTP seam: Activity records a skill get as
// query skill:<name> and an agent get as agent:<name>, in the Reference form
// <kind>:<name> (ADR 0021), hit or miss, so a missed reviewer skill and a
// missed reviewer agent differ. An article lookup keeps its bare id.

func TestActivityRecordsSkillAndAgentGetsAsKindColonName(t *testing.T) {
	pub := activityHandler(t, platform.Config{ContentDir: skillGetsContent(t), RateLimit: roomyRateLimit})
	for _, c := range []struct {
		path  string
		query string
		miss  bool
		code  int
	}{
		{"/api/v1/skills/deploy", "skill:deploy", false, http.StatusOK},
		{"/api/v1/skills/reviewer", "skill:reviewer", true, http.StatusNotFound},
		{"/api/v1/agents/reviewer", "agent:reviewer", false, http.StatusOK},
		{"/api/v1/agents/deploy", "agent:deploy", true, http.StatusNotFound},
	} {
		e := lastEvent(t, pub, c.path, c.code, nil)
		if e.Query != c.query || e.Miss != c.miss {
			t.Errorf("GET %s recorded query %q miss %v, want %q miss %v", c.path, e.Query, e.Miss, c.query, c.miss)
		}
	}
}

func TestActivityKeepsTheBareIdForAnArticleLookup(t *testing.T) {
	pub := activityHandler(t, platform.Config{})
	for path, want := range map[string]string{
		"/api/v1/articles/agent-loops":  "agent-loops",
		"/api/v1/articles/no-such-note": "no-such-note",
	} {
		code := http.StatusOK
		if want == "no-such-note" {
			code = http.StatusNotFound
		}
		e := lastEvent(t, pub, path, code, nil)
		if e.Kind != api.KindLookup || e.Query != want {
			t.Errorf("GET %s recorded kind %q query %q, want a lookup of %q", path, e.Kind, e.Query, want)
		}
	}
}
