package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// Issue #269: a content fixture that breaks a governance rule never reaches the
// API. Before any good load the server answers 503 not_ready; after one it
// keeps serving the last good Snapshot.

// writeRaw writes one file of a dir Source.
func writeRaw(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// brokenFixtures each break one rule of the lifecycle frontmatter.
func brokenFixtures() map[string]string {
	return map[string]string{
		"status not in the vocabulary": "---\nname: Broken\nstatus: Draft\n---\nbody\n",
		"enforced not a bool":          "---\nname: Broken\nenforced: yes\n---\nbody\n",
		"replaced_by dangles":          "---\nname: Broken\nreplaced_by: article:missing\n---\nbody\n",
		"replaced_by names itself":     "---\nname: Broken\nreplaced_by: article:broken\n---\nbody\n",
		"replaced_by malformed":        "---\nname: Broken\nreplaced_by: ../etc/passwd\n---\nbody\n",
	}
}

func TestBrokenGovernanceFixtureKeepsTheServerNotReady(t *testing.T) {
	for name, content := range brokenFixtures() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeRaw(t, dir, "global/articles/broken.md", content)
			syncer := engine.NewSyncer(engine.NewDirSource(dir))
			if err := syncer.Poll(t.Context()); err == nil {
				t.Fatal("the content loaded; a rule-breaking fixture must fail the load")
			}
			srv := providerServer(t, syncer.Current)
			for _, path := range readOperationPaths(t) {
				resp, body := apiGet(t, srv.URL+path)
				if resp.StatusCode != http.StatusServiceUnavailable {
					t.Errorf("GET %s: status %d, want 503: %s", path, resp.StatusCode, body)
					continue
				}
				assertEnvelope(t, body, "not_ready")
				if strings.Contains(string(body), dir) {
					t.Errorf("GET %s: body leaks the content path: %s", path, body)
				}
			}
		})
	}
}

func TestStartupRefusesAFixtureThatBreaksAGovernanceRule(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "global/articles/broken.md", "---\nname: Broken\nstatus: Draft\n---\nbody\n")
	_, err := PublicHandler(platform.Config{
		ContentDir: dir,
		APIToken:   testToken,
		RateLimit:  withDefaultRateLimit(platform.RateLimit{}),
	}, nil)
	if err == nil {
		t.Fatal("PublicHandler accepted content that breaks a rule")
	}
	if !strings.Contains(err.Error(), "global/articles/broken.md") || strings.Contains(err.Error(), dir) {
		t.Errorf("error = %q, want it to name the file relative to the content directory", err)
	}
}

func TestServerKeepsServingTheLastGoodSnapshotWhenContentBreaks(t *testing.T) {
	for name, content := range brokenFixtures() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeArticle(t, dir, "alpha", "Alpha article", "alpha body")
			syncer := engine.NewSyncer(engine.NewDirSource(dir))
			if err := syncer.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			srv := providerServer(t, syncer.Current)
			if resp, body := apiGet(t, srv.URL+"/api/v1/articles/alpha"); resp.StatusCode != http.StatusOK {
				t.Fatalf("before the change: status %d: %s", resp.StatusCode, body)
			}

			writeRaw(t, dir, "global/articles/broken.md", content)
			if err := syncer.Poll(t.Context()); err == nil {
				t.Fatal("the broken content loaded")
			}

			if resp, body := apiGet(t, srv.URL+"/api/v1/articles/alpha"); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "alpha body") {
				t.Errorf("the last good article: status %d: %s", resp.StatusCode, body)
			}
			if resp, body := apiGet(t, srv.URL+"/api/v1/articles/broken"); resp.StatusCode != http.StatusNotFound {
				t.Errorf("the broken article: status %d, want 404 (it must not be served): %s", resp.StatusCode, body)
			}
			if resp, body := apiGet(t, srv.URL+"/api/v1/search?q=alpha"); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "alpha") {
				t.Errorf("search: status %d: %s", resp.StatusCode, body)
			}
		})
	}
}
