package app

import (
	"net/http"
	"testing"
	"time"
)

// A Source of GOLDIE_SOURCES publishes only what its claims cover (#477): an
// Item outside them fails that Source's own load, and the other Sources keep
// serving. No claims deny everything.
func TestASourcePublishingOutsideItsClaimsFailsOnlyItsOwnCheck(t *testing.T) {
	good, rogue, bare := newBareRepo(t), newBareRepo(t), newBareRepo(t)
	good.commit(gitArticle("claimed-item"))
	rogue.commit(gitArticle("rogue-item"))
	bare.commit(gitArticle("unclaimed-item"))
	rogueSpec := gitSpec("rogue", rogue.url)
	rogueSpec["claims"] = map[string]any{"projects": []string{"git.example.com/team-a/*"}}
	bareSpec := gitSpec("bare", bare.url)
	delete(bareSpec, "claims")

	g := startGit(t, sourcesConfig(t, time.Hour, nil, gitSpec("good", good.url), rogueSpec, bareSpec))

	eventually(t, 5*time.Second, "source:good never became ok", func() bool {
		return checkStatus(t, g.admin, "source:good") == "ok" && served(t, g, "claimed-item")
	})
	eventually(t, 5*time.Second, "the failed loads were not logged", func() bool {
		return g.count(t, "warn", `"source":"rogue"`) > 0 && g.count(t, "warn", `"source":"bare"`) > 0
	})
	for _, name := range []string{"source:rogue", "source:bare"} {
		if st := checkStatus(t, g.admin, name); st == "" || st == "ok" {
			t.Errorf("check %s = %q, want failing: the Source publishes outside its claims", name, st)
		}
	}
	for _, id := range []string{"rogue-item", "unclaimed-item"} {
		if code, _ := pubGet(t, g.pub, "/api/v1/articles/"+id); code != http.StatusNotFound {
			t.Errorf("GET article %s = %d, want 404: an Item outside the claims is served", id, code)
		}
	}
	if n := g.count(t, "warn", `"source":"good"`); n != 0 {
		t.Errorf("warn records for the Source inside its claims = %d, want 0", n)
	}
}
