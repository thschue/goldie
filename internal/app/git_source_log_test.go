package app

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// #96: the git Source's poll records carry the URL in one form only, the one
// platform's redactor gives (Config.RedactedGitURL): the user is gone, the
// host and path stay, so the operator can tell which Source failed.
func TestGitPollRecordsCarryTheRedactedURL(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens: every poll fails at once
	host, port, _ := net.SplitHostPort(addr)
	sec := newGitSecrets(t, "["+host+"]:"+port)
	cfg := gitConfig(t, "ssh://"+sec.user+"@"+addr+"/org/content.git", time.Hour)
	cfg.GitSSHKey, cfg.GitSSHKnownHosts = sec.key, sec.knownHostsLine
	g := startGit(t, cfg)
	eventually(t, 5*time.Second, "a failed poll logged no warn", func() bool { return g.count(t, "warn") > 0 })
	want := "ssh://(redacted)@" + addr + "/org/content.git"
	if want != cfg.RedactedGitURL() {
		t.Fatalf("RedactedGitURL = %q, want %q", cfg.RedactedGitURL(), want)
	}
	for _, l := range g.logs.lines()[g.from:] {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("log line is not JSON: %v: %s", err, l)
		}
		if rec["level"] != "warn" {
			continue
		}
		if rec["url"] != want || rec["source"] != "git" {
			t.Errorf("warn record url = %v, source = %v; want %q and git: %s", rec["url"], rec["source"], want, l)
		}
	}
	assertNoGitSecret(t, "the logs", strings.Join(g.logs.lines(), "\n"), append(sec.material, sec.user)...)
}
