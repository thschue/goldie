package cli

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
		{"https://gitlab.com/group/sub/widgets.git", "gitlab.com/group/sub/widgets"},
		{"https://user:s3cret-token@github.com/acme/widgets.git", "github.com/acme/widgets"},
		{"https://s3cret-token@github.com/acme/widgets.git", "github.com/acme/widgets"},
		{"  git@github.com:acme/widgets.git  ", "github.com/acme/widgets"},
	}
	for _, c := range cases {
		got, ok := NormaliseRemote(c.in)
		if !ok || got != c.want {
			t.Errorf("NormaliseRemote(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
		if strings.Contains(got, "s3cret") {
			t.Errorf("NormaliseRemote(%q) leaks userinfo: %q", c.in, got)
		}
	}
	for _, bad := range []string{"", "/srv/git/widgets.git", "../widgets", "file:///srv/widgets.git", "https://github.com", "github.com", "https://github.com/a b/c", "user:s3cret@github.com:acme/widgets.git"} {
		if got, ok := NormaliseRemote(bad); ok {
			t.Errorf("NormaliseRemote(%q) = %q, want it rejected", bad, got)
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
		name, cfg, want, source string
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
			got, err := ResolveProject(sub, "", "")
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
	got, err := ResolveProject(t.TempDir(), "", "")
	if err != nil || got != (Project{}) {
		t.Errorf("got %+v, %v; want no Project and no error", got, err)
	}
}

func TestResolveProjectWorktreeAndSubmodule(t *testing.T) {
	main := repo(t, forkConfig)
	wtGit := filepath.Join(main, ".git", "worktrees", "feature")
	write(t, filepath.Join(wtGit, "commondir"), "../..\n")
	wt := t.TempDir()
	write(t, filepath.Join(wt, ".git"), "gitdir: "+wtGit+"\n")
	got, err := ResolveProject(wt, "", "")
	if err != nil || got.ID != "github.com/acme/widgets" || got.Source != SourceUpstream {
		t.Errorf("worktree: got %+v, %v", got, err)
	}

	// A submodule's .git file points, relatively, at a gitdir with its own config.
	super := t.TempDir()
	write(t, filepath.Join(super, ".git", "modules", "sub", "config"), "[remote \"origin\"]\n\turl = git@github.com:acme/sub.git\n")
	write(t, filepath.Join(super, "sub", ".git"), "gitdir: ../.git/modules/sub\n")
	got, err = ResolveProject(filepath.Join(super, "sub"), "", "")
	if err != nil || got.ID != "github.com/acme/sub" || got.Source != SourceOrigin {
		t.Errorf("submodule: got %+v, %v", got, err)
	}

	// A .git file that is not a gitdir pointer yields no Project, not an error.
	bad := t.TempDir()
	write(t, filepath.Join(bad, ".git"), "garbage")
	if got, err := ResolveProject(bad, "", ""); err != nil || got.ID != "" {
		t.Errorf("garbage .git file: got %+v, %v", got, err)
	}
}

func TestResolveProjectOverrideOrder(t *testing.T) {
	dir := repo(t, forkConfig)
	write(t, filepath.Join(dir, ".goldie", "config.yaml"), "project: gitlab.com/Team/Config.git\n")
	cases := []struct {
		name, flag, env, want, source string
	}{
		{"flag wins", "ex.com/flag/p", "ex.com/env/p", "ex.com/flag/p", SourceFlag},
		{"env beats config", "", "ex.com/env/p", "ex.com/env/p", SourceEnv},
		{"config beats detection", "", "", "gitlab.com/team/config", SourceConfig},
		{"url form in flag is normalised", "git@GitHub.com:A/B.git", "", "github.com/a/b", SourceFlag},
	}
	for _, c := range cases {
		got, err := ResolveProject(dir, c.flag, c.env)
		if err != nil || got.ID != c.want || got.Source != c.source {
			t.Errorf("%s: got %+v, %v; want %q from %q", c.name, got, err, c.want, c.source)
		}
	}
	if err := os.Remove(filepath.Join(dir, ".goldie", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if got, _ := ResolveProject(dir, "", ""); got.Source != SourceUpstream {
		t.Errorf("without config: got %+v, want detection", got)
	}
}

func TestResolveProjectConfigFileErrorsAndUntrustedValues(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".goldie", "config.yaml"), "project: [unclosed\n")
	if _, err := ResolveProject(dir, "", ""); err == nil {
		t.Error("invalid YAML: want an error")
	}
	write(t, filepath.Join(dir, ".goldie", "config.yaml"), "project: \"https://user:s3cret-token@\"\n")
	if _, err := ResolveProject(dir, "", ""); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("unreadable URL: err = %v, want an error that omits the credential", err)
	}
	for _, v := range []string{"user:s3cret-token@host", "a@b"} {
		if _, err := ResolveProject(dir, v, ""); err == nil || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("--project %q: err = %v", v, err)
		}
	}
}
