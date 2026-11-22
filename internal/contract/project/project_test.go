package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormaliseRemote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"git@github.com:Acme/Widgets.git", "github.com/acme/widgets"},
		{"git@github.com:acme/widgets", "github.com/acme/widgets"},
		{"ssh://git@GitHub.com/acme/widgets.git", "github.com/acme/widgets"},
		{"ssh://git@git.example.com:2222/acme/widgets.git", "git.example.com/acme/widgets"},
		{"https://github.com/acme/widgets.git", "github.com/acme/widgets"},
		{"https://github.com/acme/widgets/", "github.com/acme/widgets"},
		{"HTTPS://GitHub.com/Acme/Widgets", "github.com/acme/widgets"},
		{"git://github.com/acme/widgets.git", "github.com/acme/widgets"},
		{"https://user:s3cret-token@github.com/acme/widgets.git", "github.com/acme/widgets"},
		{"https://s3cret-token@github.com/acme/widgets.git", "github.com/acme/widgets"},
		{"  git@github.com:acme/widgets.git  ", "github.com/acme/widgets"},
	}
	for _, c := range cases {
		got, ok := normaliseRemote(c.in)
		if !ok || got != c.want {
			t.Errorf("normaliseRemote(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
		if strings.Contains(got, "s3cret") {
			t.Errorf("normaliseRemote(%q) leaks userinfo: %q", c.in, got)
		}
	}
	for _, bad := range []string{"", "/srv/git/widgets.git", "../widgets", "file:///srv/widgets.git", "https://github.com", "github.com", "https://github.com/a b/c", "user:s3cret@github.com:acme/widgets.git", "https://gitlab.com/group/sub/widgets.git"} {
		if got, ok := normaliseRemote(bad); ok {
			t.Errorf("normaliseRemote(%q) = %q, want it rejected", bad, got)
		}
	}
}

// TestProjectSegmentRules checks that a remote or a typed value only becomes a
// Project when every segment passes the engine's projects/ rules, so path
// syntax from a hostile remote ('..', escapes, '?', '#', ':') never does.
func TestProjectSegmentRules(t *testing.T) {
	for _, bad := range []string{
		"https://h.example/%2e%2e/%2e%2e/x", "https://h.example/org/re%3Fpo", "https://h.example/org/re%23po",
		"https://h.example/org/re%25po", "host.example:org\\..\\x", "https://h.example/.hidden/x",
		"https://h_x.example/org/repo", "https://h.example/org/repo%C3%A9", "https://h.example/org/repo:tok",
		"git@h.example:org/repo.git?x=1", "https://[::1]/a/b", "x:y",
	} {
		if got, ok := normaliseRemote(bad); ok {
			t.Errorf("normaliseRemote(%q) = %q, want it rejected", bad, got)
		}
	}
	for _, bad := range []string{"../../etc", "foo", "a/b/c/d", "a b/c/d", "%2e%2e/x/y", "ex.com/a/b?q", "ex.com/äö/c", "ex.com/a/../b", "gitlab.com/group/sub/app", "ex.com/org"} {
		if got, err := normaliseConfigured(bad); err == nil || strings.Contains(err.Error(), bad) {
			t.Errorf("normaliseConfigured(%q) = %q, %v; want an error that does not echo it", bad, got, err)
		}
	}
	// Detection skips a nested-group upstream and moves on to origin.
	nested := repo(t, "[remote \"upstream\"]\n\turl = https://gitlab.com/g/sub/app.git\n[remote \"origin\"]\n\turl = https://github.com/a/b\n")
	if got, err := Resolve(nested, "", ""); err != nil || got.ID != "github.com/a/b" || got.Source != SourceOrigin {
		t.Errorf("nested-group upstream: got %+v, %v; want origin", got, err)
	}
	// A config file of the wrong shape is an error; one without `project:` defers to detection.
	write(t, filepath.Join(nested, ".goldie", "config.yaml"), "project: gitlab.com/g/sub/app\n")
	if _, err := Resolve(nested, "", ""); err == nil || strings.Contains(err.Error(), "sub/app") {
		t.Errorf("nested-group config: err = %v, want a non-echoing error", err)
	}
	write(t, filepath.Join(nested, ".goldie", "config.yaml"), "other: 1\n")
	if got, err := Resolve(nested, "", ""); err != nil || got.Source != SourceOrigin {
		t.Errorf("config without project: got %+v, %v; want detection", got, err)
	}
	for in, want := range map[string]string{"Ex.com/Org_1/re-po.v2": "ex.com/org_1/re-po.v2"} {
		if got, err := normaliseConfigured(in); err != nil || got != want {
			t.Errorf("normaliseConfigured(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo makes a directory with a .git/config holding cfg and returns it.
func repo(t *testing.T, cfg string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".git", "config"), cfg)
	return dir
}

const forkConfig = `[core]
	bare = false
[remote "origin"]
	url = git@github.com:me/widgets.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[remote "upstream"]
	url = https://GitHub.com/Acme/Widgets.git
`

func TestResolveProjectFromGit(t *testing.T) {
	cases := []struct {
		name, cfg, want string
		source          Source
	}{
		{"fork prefers upstream", forkConfig, "github.com/acme/widgets", SourceUpstream},
		{"origin only", "[remote \"origin\"]\n\turl = git@gitlab.example.com:team/app.git\n", "gitlab.example.com/team/app", SourceOrigin},
		{"unusable upstream falls back to origin", "[remote \"upstream\"]\n\turl = /srv/local.git\n[remote \"origin\"]\n\turl = https://github.com/a/b\n", "github.com/a/b", SourceOrigin},
		{"no remotes", "[core]\n\tbare = false\n", "", ""},
		{"other remote only", "[remote \"mirror\"]\n\turl = https://github.com/a/b\n", "", ""},
		{"quoted url and comments", "# c\n[remote \"origin\"]\n\t; c\n\tURL = \"https://github.com/a/b.git\"\n", "github.com/a/b", SourceOrigin},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub := filepath.Join(repo(t, c.cfg), "deep", "er")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			got, err := Resolve(sub, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != c.want || got.Source != c.source {
				t.Errorf("got %+v, want %q from %q", got, c.want, c.source)
			}
		})
	}
}

func TestResolveProjectNotARepository(t *testing.T) {
	got, err := Resolve(t.TempDir(), "", "")
	if err != nil || got != (Detected{}) {
		t.Errorf("got %+v, %v; want no Project and no error", got, err)
	}
}

func TestResolveProjectWorktreeAndSubmodule(t *testing.T) {
	main := repo(t, forkConfig)
	wtGit := filepath.Join(main, ".git", "worktrees", "feature")
	write(t, filepath.Join(wtGit, "commondir"), "../..\n")
	wt := t.TempDir()
	write(t, filepath.Join(wt, ".git"), "gitdir: "+wtGit+"\n")
	got, err := Resolve(wt, "", "")
	if err != nil || got.ID != "github.com/acme/widgets" || got.Source != SourceUpstream {
		t.Errorf("worktree: got %+v, %v", got, err)
	}

	// A submodule's .git file points, relatively, at a gitdir with its own config.
	super := t.TempDir()
	write(t, filepath.Join(super, ".git", "modules", "sub", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/sub.git\n")
	write(t, filepath.Join(super, "sub", ".git"), "gitdir: ../.git/modules/sub\n")
	got, err = Resolve(filepath.Join(super, "sub"), "", "")
	if err != nil || got.ID != "github.com/acme/sub" || got.Source != SourceOrigin {
		t.Errorf("submodule: got %+v, %v", got, err)
	}

	// A .git file that is not a gitdir pointer yields no Project, not an error.
	bad := t.TempDir()
	write(t, filepath.Join(bad, ".git"), "garbage")
	if got, err := Resolve(bad, "", ""); err != nil || got.ID != "" {
		t.Errorf("garbage .git file: got %+v, %v", got, err)
	}
}

func TestResolveProjectOverrideOrder(t *testing.T) {
	dir := repo(t, forkConfig)
	write(t, filepath.Join(dir, ".goldie", "config.yaml"), "project: gitlab.com/Team/Config.git\n")
	cases := []struct {
		name, flag, env, want string
		source                Source
	}{
		{"flag wins", "ex.com/flag/p", "ex.com/env/p", "ex.com/flag/p", SourceFlag},
		{"env beats config", "", "ex.com/env/p", "ex.com/env/p", SourceEnv},
		{"config beats detection", "", "", "gitlab.com/team/config", SourceConfig},
		{"url form in flag is normalised", "git@GitHub.com:A/B.git", "", "github.com/a/b", SourceFlag},
	}
	for _, c := range cases {
		got, err := Resolve(dir, c.flag, c.env)
		if err != nil || got.ID != c.want || got.Source != c.source {
			t.Errorf("%s: got %+v, %v; want %q from %q", c.name, got, err, c.want, c.source)
		}
	}
	if err := os.Remove(filepath.Join(dir, ".goldie", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if got, _ := Resolve(dir, "", ""); got.Source != SourceUpstream {
		t.Errorf("without config: got %+v, want detection", got)
	}
}

// TestResolveProjectConfigFileStopsAtRepositoryRoot checks that a repository
// uses its own .goldie/config.yaml, never one of a checkout it is nested in.
func TestResolveProjectConfigFileStopsAtRepositoryRoot(t *testing.T) {
	outer := repo(t, "[remote \"origin\"]\n\turl = https://evil.example/other/outer\n")
	write(t, filepath.Join(outer, ".goldie", "config.yaml"), "project: evil.example/other/repo\n")
	inner := filepath.Join(outer, "vendor", "inner")
	write(t, filepath.Join(inner, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/inner.git\n")
	sub := filepath.Join(inner, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(sub, "", ""); err != nil || got.ID != "github.com/acme/inner" || got.Source != SourceOrigin {
		t.Errorf("nested clone: got %+v, %v; want its own origin", got, err)
	}
	// The repository's own config is found from a subdirectory.
	write(t, filepath.Join(inner, ".goldie", "config.yaml"), "project: github.com/acme/configured\n")
	if got, err := Resolve(sub, "", ""); err != nil || got.ID != "github.com/acme/configured" || got.Source != SourceConfig {
		t.Errorf("own config from a subdirectory: got %+v, %v", got, err)
	}
	// Outside a repository only the working directory's config counts.
	plain := t.TempDir()
	write(t, filepath.Join(plain, ".goldie", "config.yaml"), "project: evil.example/other/repo\n")
	below := filepath.Join(plain, "below")
	if err := os.MkdirAll(below, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(below, "", ""); err != nil || got.ID != "" {
		t.Errorf("outside a repository: got %+v, %v; want none", got, err)
	}
	if got, err := Resolve(plain, "", ""); err != nil || got.Source != SourceConfig {
		t.Errorf("working directory config outside a repository: got %+v, %v", got, err)
	}
}

// TestResolveProjectOversizedFiles checks the maxProjectFile cap: an
// oversized .git/config yields no Project, an oversized .goldie/config.yaml
// is an error.
func TestResolveProjectOversizedFiles(t *testing.T) {
	pad := "# " + strings.Repeat("x", maxProjectFile) + "\n"
	big := repo(t, "[remote \"origin\"]\n\turl = https://github.com/a/b\n"+pad)
	if got, err := Resolve(big, "", ""); err != nil || got.ID != "" {
		t.Errorf("oversized .git/config: got %+v, %v; want no Project", got, err)
	}
	dir := repo(t, "[remote \"origin\"]\n\turl = https://github.com/a/b\n")
	write(t, filepath.Join(dir, ".goldie", "config.yaml"), "project: github.com/c/d\n"+pad)
	if _, err := Resolve(dir, "", ""); err == nil || !strings.Contains(err.Error(), "cannot read .goldie/config.yaml") {
		t.Errorf("oversized .goldie/config.yaml: err = %v, want the read error", err)
	}
	// Exactly at the cap is still read.
	cfg := "[remote \"origin\"]\n\turl = https://github.com/a/b\n"
	exact := repo(t, cfg+strings.Repeat("#", maxProjectFile-len(cfg)))
	if got, err := Resolve(exact, "", ""); err != nil || got.ID != "github.com/a/b" {
		t.Errorf(".git/config at the cap: got %+v, %v", got, err)
	}
}

func TestResolveProjectConfigFileErrorsAndUntrustedValues(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".goldie", "config.yaml"), "project: [unclosed\n")
	if _, err := Resolve(dir, "", ""); err == nil {
		t.Error("invalid YAML: want an error")
	}
	write(t, filepath.Join(dir, ".goldie", "config.yaml"), "project: \"https://user:s3cret-token@\"\n")
	if _, err := Resolve(dir, "", ""); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("unreadable URL: err = %v, want an error that omits the credential", err)
	}
	for _, v := range []string{"user:s3cret-token@host", "a@b"} {
		if _, err := Resolve(dir, v, ""); err == nil || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("--project %q: err = %v", v, err)
		}
	}
}
