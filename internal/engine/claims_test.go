package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #477: each Source contributes only what it claims (CONTEXT: Claim).
// engine.Claim{Global bool, Projects []string} is the Source's Claim and
// engine.NewClaimedSource(base Source, claim Claim) Source decorates a Source
// with it, as NewLocalSource does (parsing Claims from config is #478; tests
// construct them directly). Deny by default: the zero Claim accepts nothing.
// Global true accepts global Items; a Project's Items need a matching Projects
// glob, where * stands for one path segment and matching ignores case (like
// lookupProject). An Item outside the Claim fails the whole load with an error
// naming the file relative to the Source root, and the Syncer keeps serving the
// last good Snapshot. A Project directory with no Items does not fail, and the
// local Scope (.goldie/) is not subject to Claims.

const (
	claimGlobalFile = "global/articles/rules.md"
	claimAPIFile    = "projects/github.com/acme/api/articles/api.md"
	claimOtherFile  = "projects/github.com/other/x/articles/other.md"
	claimOtherAgent = "projects/github.com/other/x/agents/bot.md"
	claimAcmeGlob   = "github.com/acme/*"
	claimAPIProject = "github.com/acme/api"
)

func claimLoad(t *testing.T, claim engine.Claim, files map[string]string) *engine.Snapshot {
	t.Helper()
	src := engine.NewClaimedSource(engine.NewDirSource(writeFiles(t, files)), claim)
	snap, err := src.Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap == nil {
		t.Fatal("Load returned no Snapshot and no error")
	}
	return snap
}

// claimLoadErr loads files under claim and returns the error, which must name
// file relative to the Source root and no host path.
func claimLoadErr(t *testing.T, claim engine.Claim, files map[string]string, file string) string {
	t.Helper()
	dir := writeFiles(t, files)
	snap, err := engine.NewClaimedSource(engine.NewDirSource(dir), claim).Load(t.Context())
	if err == nil {
		t.Fatalf("Load succeeded, want an error naming %s: %+v", file, snap.Articles())
	}
	msg := err.Error()
	if !strings.Contains(msg, file) {
		t.Errorf("error %q does not name %s", msg, file)
	}
	if strings.Contains(msg, dir) {
		t.Errorf("error %q leaks the host path %s", msg, dir)
	}
	return msg
}

func TestClaimEmptyLoadsNoItems(t *testing.T) {
	// A Source with nothing to load is fine under an empty Claim.
	snap := claimLoad(t, engine.Claim{}, map[string]string{})
	if got := snap.Articles(); len(got) != 0 {
		t.Fatalf("Articles = %+v, want none", got)
	}
}

func TestClaimEmptyRejectsEveryItem(t *testing.T) {
	for name, file := range map[string]string{
		"global article":  claimGlobalFile,
		"project article": claimAPIFile,
		"project agent":   claimOtherAgent,
	} {
		t.Run(name, func(t *testing.T) {
			claimLoadErr(t, engine.Claim{}, map[string]string{file: article("X")}, file)
		})
	}
}

func TestClaimGlobalAcceptsGlobalItemsOnly(t *testing.T) {
	claim := engine.Claim{Global: true}
	snap := claimLoad(t, claim, map[string]string{claimGlobalFile: article("Rules")})
	if _, ok := snap.Article("rules"); !ok {
		t.Fatal("global Item rules missing under global: true")
	}
	// Global does not imply any Project.
	claimLoadErr(t, claim, map[string]string{
		claimGlobalFile: article("Rules"),
		claimAPIFile:    article("Api"),
	}, claimAPIFile)
}

func TestClaimProjectsAloneRejectGlobalItems(t *testing.T) {
	claimLoadErr(t, engine.Claim{Projects: []string{claimAcmeGlob}}, map[string]string{
		claimGlobalFile: article("Rules"),
	}, claimGlobalFile)
}

func TestClaimProjectGlobMatchesOneSegment(t *testing.T) {
	claim := engine.Claim{Projects: []string{claimAcmeGlob}}
	snap := claimLoad(t, claim, map[string]string{claimAPIFile: article("Api")})
	if a, ok := snap.ArticleIn(claimAPIProject, "api"); !ok || a.Scope != claimAPIProject {
		t.Fatalf("ArticleIn(%s, api) = %+v, %v", claimAPIProject, a, ok)
	}
	claimLoadErr(t, claim, map[string]string{claimOtherFile: article("Other")}, claimOtherFile)
}

func TestClaimProjectsExactAndMultipleGlobs(t *testing.T) {
	claim := engine.Claim{Projects: []string{claimAPIProject, "github.com/other/*"}}
	snap := claimLoad(t, claim, map[string]string{
		claimAPIFile:   article("Api"),
		claimOtherFile: article("Other"),
	})
	if got := snap.Projects(); len(got) != 2 {
		t.Fatalf("Projects = %v, want both", got)
	}
	web := "projects/github.com/acme/web/articles/web.md"
	claimLoadErr(t, claim, map[string]string{web: article("Web")}, web)
}

func TestClaimMatchingIgnoresCase(t *testing.T) {
	// Claim in upper case against a mixed-case directory, and the reverse.
	for _, claim := range []string{"GitHub.com/ACME/*", "github.com/acme/api"} {
		snap := claimLoad(t, engine.Claim{Projects: []string{claim}}, map[string]string{
			"projects/github.com/Acme/API/articles/api.md": article("Api"),
		})
		if got := snap.Projects(); len(got) != 1 {
			t.Errorf("claim %q: Projects = %v, want the Acme/API Project", claim, got)
		}
	}
	claimLoadErr(t, engine.Claim{Projects: []string{"GITHUB.COM/ACME/*"}}, map[string]string{
		claimOtherFile: article("Other"),
	}, claimOtherFile)
}

func TestClaimGlobDoesNotMatchPrefixOrAcrossSegments(t *testing.T) {
	// "github.com/acme/*" must not accept a longer org that starts with acme.
	file := "projects/github.com/acmecorp/api/articles/api.md"
	claimLoadErr(t, engine.Claim{Projects: []string{claimAcmeGlob}}, map[string]string{file: article("Api")}, file)
	// A * does not swallow slashes.
	claimLoadErr(t, engine.Claim{Projects: []string{"github.com/*"}}, map[string]string{claimAPIFile: article("Api")}, claimAPIFile)
}

func TestClaimedAgentOutsideClaimFailsLoad(t *testing.T) {
	claim := engine.Claim{Global: true, Projects: []string{claimAcmeGlob}}
	claimLoadErr(t, claim, map[string]string{claimOtherAgent: article("Bot")}, claimOtherAgent)
}

func TestClaimOneBadItemFailsWholeLoad(t *testing.T) {
	claim := engine.Claim{Global: true, Projects: []string{claimAcmeGlob}}
	msg := claimLoadErr(t, claim, map[string]string{
		claimGlobalFile: article("Rules"),
		claimAPIFile:    article("Api"),
		claimOtherFile:  article("Other"),
	}, claimOtherFile)
	if strings.Contains(msg, claimGlobalFile) || strings.Contains(msg, claimAPIFile) {
		t.Errorf("error %q names a claimed file", msg)
	}
}

func TestClaimProjectDirectoryWithoutItemsDoesNotFail(t *testing.T) {
	// A Project directory holding no Items is not an Item outside the Claim.
	dir := writeFiles(t, map[string]string{claimGlobalFile: article("Rules")})
	if err := os.MkdirAll(filepath.Join(dir, "projects/github.com/other/x/articles"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := engine.NewClaimedSource(engine.NewDirSource(dir), engine.Claim{Global: true})
	snap, err := src.Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := snap.Article("rules"); !ok {
		t.Fatal("global Item rules missing")
	}
}

func TestClaimLocalScopeIsNotSubjectToClaims(t *testing.T) {
	content := writeFiles(t, map[string]string{})
	repo := writeFiles(t, map[string]string{localFile("mine"): article("Mine")})
	src := engine.NewLocalSource(
		engine.NewClaimedSource(engine.NewDirSource(content), engine.Claim{}),
		filepath.Join(repo, ".goldie"))
	snap, err := src.Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if a, ok := snap.Article("mine"); !ok || a.Scope != engine.ScopeLocal {
		t.Fatalf("Article(mine) = %+v, %v; want the local Item", a, ok)
	}
}

func TestClaimFailureKeepsLastGoodSnapshotAndNamesFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{claimGlobalFile: article("Rules")})
	s := engine.NewSyncer(engine.NewClaimedSource(engine.NewDirSource(dir), engine.Claim{Global: true}))
	if err := s.Poll(t.Context()); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	good := s.Current()
	if good == nil {
		t.Fatal("no Snapshot after a good load")
	}

	bad := filepath.Join(dir, filepath.FromSlash(claimOtherFile))
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(article("Other")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Poll(t.Context()); err == nil {
		t.Fatal("Poll succeeded with an Item outside the Claim")
	}
	if s.Current() != good {
		t.Error("the last good Snapshot was replaced after a failed load")
	}
	st := s.Status()
	if !strings.Contains(st.LastError, claimOtherFile) {
		t.Errorf("LastError %q does not name %s", st.LastError, claimOtherFile)
	}
	if strings.Contains(st.LastError, dir) {
		t.Errorf("LastError %q leaks the host path", st.LastError)
	}

	// Fixing the Source recovers.
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := s.Poll(t.Context()); err != nil {
		t.Fatalf("Poll after the fix: %v", err)
	}
	if st := s.Status(); st.LastError != "" {
		t.Errorf("LastError = %q after a good load", st.LastError)
	}
}
