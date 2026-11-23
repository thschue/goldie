package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// optionOf returns the option a NewGitSource error names, failing the test
// when the error is not a *GitOptionError.
func optionOf(t *testing.T, err error) engine.GitOption {
	t.Helper()
	var oe *engine.GitOptionError
	if !errors.As(err, &oe) {
		t.Fatalf("err = %v (%T), want a *engine.GitOptionError", err, err)
	}
	return oe.Option
}

// #96: an scp-like URL, user@host:path as every forge shows it, is an ssh URL:
// it needs SSHKey and KnownHosts like ssh://, and is accepted with them,
// optionally with go-git's :port: form.
func TestGitSourceAcceptsSCPLikeURLs(t *testing.T) {
	key, host := newSSHKey(t), newSSHKey(t)
	kh := "git.example.invalid " + authorizedKey(host.pub)
	for _, raw := range []string{
		"git@git.example.invalid:org/r.git",
		"deploy@git.example.invalid:/srv/git/r.git",
		"git@git.example.invalid:2222:org/r.git",
		"git_user.x@git-host.example.invalid:r",
	} {
		if _, err := engine.NewGitSource(engine.GitOptions{URL: raw, SSHKey: key.pem, KnownHosts: kh}); err != nil {
			t.Errorf("%q: %v, want it accepted", raw, err)
		}
		_, err := engine.NewGitSource(engine.GitOptions{URL: raw, KnownHosts: kh})
		if err == nil || optionOf(t, err) != engine.GitOptionSSHKey {
			t.Errorf("%q without SSHKey: %v, want an error naming SSHKey", raw, err)
		}
		_, err = engine.NewGitSource(engine.GitOptions{URL: raw, SSHKey: key.pem, KnownHosts: kh, Token: "tok-secret"})
		if err == nil || optionOf(t, err) != engine.GitOptionToken {
			t.Errorf("%q with a Token: %v, want an error naming Token", raw, err)
		}
	}
}

// URLs go-git would read differently from what the checks saw, or that only
// look like a git URL, are refused naming URL with a fixed message: file: and
// https: without "//" (go-git reads both as scp-like, an ssh connection to the
// host "file" or "https" with no key set), scp-like without a user, git's
// <transport>:: remote helpers, a password in the scp-like user, a user or
// host that starts with "-", and a port out of range.
func TestGitSourceRefusesAmbiguousURLs(t *testing.T) {
	key, host := newSSHKey(t), newSSHKey(t)
	kh := "git.example.invalid " + authorizedKey(host.pub)
	for _, raw := range []string{
		"file:/srv/c4n4ry/r.git",
		"FILE:/srv/c4n4ry/r.git",
		"file:c4n4ry/r.git",
		"https:/git.example.invalid/c4n4ry.git",
		"git.example.invalid:org/c4n4ry.git",
		"ext::sh -c touch% /tmp/c4n4ry",
		"fd::17",
		"ssh::git@git.example.invalid/c4n4ry",
		"git@git.example.invalid::c4n4ry",
		"deploy:pw-c4n4ry@git.example.invalid:org/r.git",
		"deploy:/pw-c4n4ry@git.example.invalid:org/r.git",
		"git@-oProxyCommand=c4n4ry:org/r.git",
		"ssh://git@-oProxyCommand=c4n4ry/org/r.git",
		"git@git.example.invalid:99999:org/c4n4ry.git",
		"git@git.example.invalid:0:org/c4n4ry.git",
		"us er@git.example.invalid:org/c4n4ry.git",
		"git@git example.invalid:org/c4n4ry.git",
		"a@b@git.example.invalid:c4n4ry",
		"./c4n4ry/r.git",
		"/srv/c4n4ry/r.git",
	} {
		_, err := engine.NewGitSource(engine.GitOptions{URL: raw, SSHKey: key.pem, KnownHosts: kh})
		if err == nil {
			t.Errorf("%q: accepted", raw)
			continue
		}
		if opt := optionOf(t, err); opt != engine.GitOptionURL {
			t.Errorf("%q: error names %s, want URL: %v", raw, opt, err)
		}
		assertNoSecret(t, err.Error(), "c4n4ry", "git.example.invalid")
	}
}

// A URL or a Ref that starts with "-", which a git command line would read as
// an option, is refused naming it; a dash later on is fine.
func TestGitSourceRefusesALeadingDash(t *testing.T) {
	for _, raw := range []string{"-oProxyCommand=c4n4ry", "--upload-pack=c4n4ry", "-https://git.example.invalid/r.git"} {
		_, err := engine.NewGitSource(engine.GitOptions{URL: raw})
		if err == nil || optionOf(t, err) != engine.GitOptionURL {
			t.Errorf("URL %q: %v, want an error naming URL", raw, err)
		}
		if err != nil && strings.Contains(err.Error(), "c4n4ry") {
			t.Errorf("URL %q: the error echoes it: %v", raw, err)
		}
	}
	const url = "https://git.example.invalid/r.git"
	for _, ref := range []string{"-", "-main", "--output=/tmp/c4n4ry"} {
		_, err := engine.NewGitSource(engine.GitOptions{URL: url, Ref: ref})
		if err == nil || optionOf(t, err) != engine.GitOptionRef {
			t.Errorf("Ref %q: %v, want an error naming Ref", ref, err)
		}
	}
	for _, ref := range []string{"feature-x", "refs/heads/release-1", "v1.0.0-rc.1"} {
		if _, err := engine.NewGitSource(engine.GitOptions{URL: url, Ref: ref}); err != nil {
			t.Errorf("Ref %q: %v, want it accepted", ref, err)
		}
	}
}

// Every NewGitSource error names the option to fix, and a bad Ref's message
// does not echo the ref.
func TestEveryGitSourceErrorNamesItsOption(t *testing.T) {
	key, host := newSSHKey(t), newSSHKey(t)
	kh := "git.example.invalid " + authorizedKey(host.pub)
	const https, sshURL = "https://git.example.invalid/r.git", "ssh://git@git.example.invalid/r.git"
	for _, tc := range []struct {
		want engine.GitOption
		opts engine.GitOptions
	}{
		{engine.GitOptionURL, engine.GitOptions{}},
		{engine.GitOptionURL, engine.GitOptions{URL: "http://git.example.invalid/r.git"}},
		{engine.GitOptionRef, engine.GitOptions{URL: https, Ref: "a..c4n4ry"}},
		{engine.GitOptionToken, engine.GitOptions{URL: sshURL, Token: "t", SSHKey: key.pem, KnownHosts: kh}},
		{engine.GitOptionSSHKey, engine.GitOptions{URL: https, SSHKey: key.pem}},
		{engine.GitOptionSSHKey, engine.GitOptions{URL: sshURL, SSHKey: "not a key", KnownHosts: kh}},
		{engine.GitOptionKnownHosts, engine.GitOptions{URL: https, KnownHosts: kh}},
		{engine.GitOptionKnownHosts, engine.GitOptions{URL: sshURL, SSHKey: key.pem}},
		{engine.GitOptionKnownHosts, engine.GitOptions{URL: sshURL, SSHKey: key.pem, KnownHosts: "not known hosts"}},
		{engine.GitOptionCABundle, engine.GitOptions{URL: sshURL, CABundle: "x", SSHKey: key.pem, KnownHosts: kh}},
		{engine.GitOptionCABundle, engine.GitOptions{URL: https, CABundle: "not a bundle"}},
	} {
		_, err := engine.NewGitSource(tc.opts)
		if err == nil {
			t.Errorf("%v: accepted", tc.opts)
			continue
		}
		if got := optionOf(t, err); got != tc.want {
			t.Errorf("%v: error names %s, want %s: %v", tc.opts, got, tc.want, err)
		}
		assertNoSecret(t, err.Error(), "c4n4ry")
	}
}
