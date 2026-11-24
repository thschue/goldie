package app

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// #479: the server serves every Source of GOLDIE_SOURCES, one Syncer each,
// as one merged view. One failing Source never stops the others. /readyz has
// one check per Source, source:<name>, failing only while that Source has
// never loaded (PC-02); the GOLDIE_CONTENT_DIR and GOLDIE_GIT_URL shorthands
// keep source:dir and source:git. Drain stops every Syncer, and each Source's
// log lines carry its name and its URL redacted.
//
// These tests use file:// repositories, so they assume GOLDIE_GIT_ALLOW_FILE_URL
// applies to every git Source of the list (ADR 0023), and that the Sources
// poll at GOLDIE_POLL_INTERVAL unless an entry sets poll.

// sourcesConfig is a loaded Config whose Sources are specs (each a JSON
// object), polled every interval. env adds variables to the environment.
func sourcesConfig(t *testing.T, interval time.Duration, env map[string]string, specs ...map[string]any) platform.Config {
	t.Helper()
	doc, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{
		platform.EnvSources:          string(doc),
		platform.EnvGitAllowFileURL:  "true",
		platform.EnvAPIToken:         testToken,
		platform.EnvAddr:             "127.0.0.1:0",
		platform.EnvAdminAddr:        "127.0.0.1:0",
		platform.EnvDrainSeconds:     "0",
		platform.EnvSwaggerUIEnabled: "false",
	}
	for k, v := range env {
		vars[k] = v
	}
	cfg := loadedConfig(t, vars)
	cfg.PollInterval = interval
	return cfg
}

// gitSpec is a git Source entry at url.
func gitSpec(name, url string) map[string]any {
	return map[string]any{"name": name, "type": "git", "url": url, "claims": map[string]any{"global": true}}
}

// checkStatus is the status of the readiness check name ("" when unlisted).
func checkStatus(t *testing.T, admin, name string) string {
	t.Helper()
	_, _, checks := readyz(t, admin)
	for _, c := range checks {
		if c.Name == name {
			return c.Status
		}
	}
	return ""
}

// served reports whether GET /api/v1/articles/id answers 200.
func served(t *testing.T, g *runningGit, id string) bool {
	t.Helper()
	code, _ := pubGet(t, g.pub, "/api/v1/articles/"+id)
	return code == http.StatusOK
}

// readyOn waits for /readyz to answer 200 with every name listed ok.
func readyOn(t *testing.T, g *runningGit, names ...string) {
	t.Helper()
	eventually(t, 5*time.Second, "/readyz never answered 200 with "+strings.Join(names, ", ")+" ok", func() bool {
		code, _, checks := readyz(t, g.admin)
		if code != http.StatusOK || len(checks) != len(names) {
			return false
		}
		for i, n := range names {
			if checks[i].Name != n || checks[i].Status != "ok" {
				return false
			}
		}
		return true
	})
}

// Two Sources of GOLDIE_SOURCES, a git Source and a dir Source, serve one
// merged view: each one's Items answer on the API.
func TestTwoSourcesServeOneMergedView(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gitArticle("from-git"))
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "global", "articles"), 0o750); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: from-dir\nkeywords: [dir]\n---\nBody of from-dir.\n"
	if err := os.WriteFile(filepath.Join(dir, "global", "articles", "from-dir.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	g := startGit(t, sourcesConfig(t, time.Hour, nil,
		gitSpec("team", repo.url),
		map[string]any{"name": "local", "type": "dir", "dir": dir, "claims": map[string]any{"global": true}},
	))
	readyOn(t, g, "source:team", "source:local")
	for _, id := range []string{"from-git", "from-dir"} {
		if code, body := pubGet(t, g.pub, "/api/v1/articles/"+id); code != http.StatusOK {
			t.Errorf("GET article %s = %d %s, want 200 from the merged view", id, code, body)
		}
	}
}

// Breaking one Source's remote leaves its last good Snapshot serving, keeps
// its check ok (PC-02), and the other Source keeps syncing.
func TestABrokenSourceKeepsItsSnapshotAndTheOtherKeepsSyncing(t *testing.T) {
	shortPolls(t)
	a, b := newBareRepo(t), newBareRepo(t)
	a.commit(gitArticle("a-first"))
	b.commit(gitArticle("b-first"))
	g := startGit(t, sourcesConfig(t, 30*time.Millisecond, nil, gitSpec("alpha", a.url), gitSpec("beta", b.url)))
	readyOn(t, g, "source:alpha", "source:beta")

	if err := os.RemoveAll(b.dir); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "the broken Source's failed poll logged no warn", func() bool {
		return g.count(t, "warn", `"source":"beta"`) > 0
	})
	a.commit(with(gitArticle("a-first"), gitArticle("a-second")))
	eventually(t, 5*time.Second, "the healthy Source stopped syncing", func() bool { return served(t, g, "a-second") })

	if !served(t, g, "b-first") {
		t.Error("the broken Source's last good Snapshot is not served any more")
	}
	if n := g.count(t, "warn", `"source":"alpha"`); n != 0 {
		t.Errorf("warn records for the healthy Source = %d, want 0", n)
	}
	readyOn(t, g, "source:alpha", "source:beta")
}

// /readyz has one check per Source, named source:<name>, and one Source that
// has never loaded fails only its own check, so /readyz answers 503 while the
// loaded Source's check is ok.
func TestReadyzHasOneCheckPerSourceFailingOnlyWhileItNeverLoaded(t *testing.T) {
	good := newBareRepo(t)
	good.commit(gitArticle("good-first"))
	missing := "file://" + t.TempDir() + "/no-such-repo.git"
	g := startGit(t, sourcesConfig(t, time.Hour, nil, gitSpec("good", good.url), gitSpec("never", missing)))

	eventually(t, 5*time.Second, "source:good never became ok", func() bool {
		return checkStatus(t, g.admin, "source:good") == "ok"
	})
	code, raw, checks := readyz(t, g.admin)
	if code != http.StatusServiceUnavailable {
		t.Errorf("/readyz with a Source never loaded = %d %s, want 503", code, raw)
	}
	status := map[string]string{}
	for _, c := range checks {
		status[c.Name] = c.Status
	}
	if len(checks) != 2 || status["source:good"] != "ok" || status["source:never"] == "" || status["source:never"] == "ok" {
		t.Errorf("/readyz checks = %s, want exactly source:good ok and source:never failing", raw)
	}
	if strings.Contains(raw, "no-such-repo") || strings.Contains(raw, "rror") {
		t.Errorf("/readyz echoes the failure: %s", raw)
	}
}

// The shorthands keep their one check: GOLDIE_CONTENT_DIR lists only
// source:dir and GOLDIE_GIT_URL only source:git.
func TestShorthandsKeepTheirReadinessCheck(t *testing.T) {
	readyOn(t, startGit(t, publicConfig(t)), "source:dir")

	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	cfg := gitConfig(t, repo.url, time.Hour)
	cfg.GitAllowFileURL = true
	readyOn(t, startGit(t, cfg), "source:git")
}

// Drain stops every Syncer: after the server context ends, a commit to either
// Source's remote is never served, while the last good content stays served.
func TestDrainStopsEverySourcesPolling(t *testing.T) {
	shortPolls(t)
	a, b := newBareRepo(t), newBareRepo(t)
	a.commit(gitArticle("a-first"))
	b.commit(gitArticle("b-first"))
	g := startGit(t, sourcesConfig(t, 30*time.Millisecond, nil, gitSpec("alpha", a.url), gitSpec("beta", b.url)))
	readyOn(t, g, "source:alpha", "source:beta")
	g.cancel()
	time.Sleep(200 * time.Millisecond) // any poll in flight ends
	a.commit(with(gitArticle("a-first"), gitArticle("a-after-stop")))
	b.commit(with(gitArticle("b-first"), gitArticle("b-after-stop")))
	time.Sleep(300 * time.Millisecond)
	for _, id := range []string{"a-after-stop", "b-after-stop"} {
		if served(t, g, id) {
			t.Errorf("article %s, committed after drain, is served: a Syncer is still polling", id)
		}
	}
	for _, id := range []string{"a-first", "b-first"} {
		if !served(t, g, id) {
			t.Errorf("article %s, the last good content, is not served after drain", id)
		}
	}
}

// Each Source's poll records carry its name and its URL redacted: the user is
// gone, host and path stay. No key material or user name reaches the logs or
// /readyz, and the other Source's records are separate.
func TestPerSourceLogLinesRedactTheURL(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens: every poll fails at once
	host, port, _ := net.SplitHostPort(addr)
	sec := newGitSecrets(t, "["+host+"]:"+port)
	good := newBareRepo(t)
	good.commit(gitArticle("good-first"))
	remote := gitSpec("remote", "ssh://"+sec.user+"@"+addr+"/org/content.git")
	remote["ssh_key_env"] = "GOLDIE_TEST_REMOTE_KEY"
	cfg := sourcesConfig(t, time.Hour, map[string]string{
		"GOLDIE_TEST_REMOTE_KEY":     sec.key,
		platform.EnvGitSSHKnownHosts: sec.knownHostsLine,
	}, gitSpec("good", good.url), remote)
	g := startGit(t, cfg)
	eventually(t, 5*time.Second, "the unreachable Source's failed poll logged no warn", func() bool {
		return g.count(t, "warn", `"source":"remote"`) > 0
	})
	want := "ssh://(redacted)@" + addr + "/org/content.git"
	found := false
	for _, l := range g.logs.lines()[g.from:] {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v: %s", err, l)
		}
		if rec["source"] == "remote" && rec["level"] == "warn" {
			found = true
			if rec["url"] != want {
				t.Errorf("warn record url = %v, want %q: %s", rec["url"], want, l)
			}
		}
		if rec["source"] == "good" && rec["level"] == "warn" {
			t.Errorf("the healthy Source logged a warn: %s", l)
		}
	}
	if !found {
		t.Error("no warn record names the failing Source")
	}
	assertNoGitSecret(t, "the logs", strings.Join(g.logs.lines(), "\n"), append(sec.material, sec.user)...)
	_, raw, _ := readyz(t, g.admin)
	assertNoGitSecret(t, "/readyz", raw, append(sec.material, sec.user, addr)...)
}
