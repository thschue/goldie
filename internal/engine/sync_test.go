package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSource struct {
	mu   sync.Mutex
	snap *Snapshot
	err  error
	rev  string
	load chan struct{} // signalled after each Load, if non-nil
}

func (f *fakeSource) set(snap *Snapshot, err error, rev string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap, f.err, f.rev = snap, err, rev
}

func (f *fakeSource) Load(context.Context) (*Snapshot, error) {
	f.mu.Lock()
	snap, err := f.snap, f.err
	f.mu.Unlock()
	if f.load != nil {
		defer func() { f.load <- struct{}{} }()
	}
	return snap, err
}

func (f *fakeSource) Revision() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rev
}

func snapOf(ids ...string) *Snapshot {
	var a []Article
	for _, id := range ids {
		a = append(a, Article{ID: id})
	}
	return newSnapshot(a)
}

func TestSyncerPollSwapAndKeepLastGood(t *testing.T) {
	src := &fakeSource{}
	s := NewSyncer(src)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	if s.Current() != nil {
		t.Fatal("Current must be nil before the first load")
	}

	src.set(nil, errors.New("read /srv/secret/path failed: https://u:p@host/repo token=abc123"), "")
	if err := s.Poll(t.Context()); err == nil {
		t.Fatal("want error")
	}
	if s.Current() != nil {
		t.Fatal("Current must stay nil after a failed first load")
	}
	st := s.Status()
	if !st.LastPoll.Equal(clock) || !st.LastSuccess.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	for _, leak := range []string{"/srv", "https://", "abc123", "u:p"} {
		if strings.Contains(st.LastError, leak) {
			t.Fatalf("LastError %q leaks %q", st.LastError, leak)
		}
	}
	if st.LastError == "" {
		t.Fatal("LastError must be recorded")
	}

	first := snapOf("a")
	src.set(first, nil, "rev1")
	clock = clock.Add(time.Minute)
	if err := s.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.Current() != first {
		t.Fatal("Current must be the loaded Snapshot")
	}
	st = s.Status()
	if st.Revision != "rev1" || st.LastError != "" || !st.LastSuccess.Equal(clock) {
		t.Fatalf("status = %+v", st)
	}

	src.set(nil, errors.New("boom"), "")
	clock = clock.Add(time.Minute)
	if err := s.Poll(t.Context()); err == nil {
		t.Fatal("want error")
	}
	if s.Current() != first {
		t.Fatal("a failed load must keep the last good Snapshot")
	}
	st = s.Status()
	if st.Revision != "rev1" || st.LastError != "boom" || !st.LastPoll.Equal(clock) || st.LastSuccess.Equal(clock) {
		t.Fatalf("status = %+v", st)
	}

	second := snapOf("a", "b")
	src.set(second, nil, "rev2")
	if err := s.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.Current() != second || s.Status().Revision != "rev2" {
		t.Fatal("success must swap the Snapshot and revision")
	}
}

func TestSyncerNilSnapshotIsFailure(t *testing.T) {
	s := NewSyncer(&fakeSource{})
	if err := s.Poll(t.Context()); err == nil || s.Current() != nil {
		t.Fatal("a nil Snapshot without error must count as a failure")
	}
}

// plainSource has no Revision method, like DirSource.
type plainSource struct{}

func (plainSource) Load(context.Context) (*Snapshot, error) { return snapOf(), nil }

func TestSyncerRevisionEmptyWithoutRevisioner(t *testing.T) {
	s := NewSyncer(plainSource{})
	if err := s.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := s.Status().Revision; r != "" {
		t.Fatalf("revision = %q", r)
	}
}

func TestSyncerRunLoadsImmediatelyThenOnTickAndStopsOnCancel(t *testing.T) {
	src := &fakeSource{load: make(chan struct{}, 8)}
	src.set(snapOf("a"), nil, "r")
	s := NewSyncer(src)
	ticks := make(chan time.Time)
	stopped := make(chan struct{})
	s.newTicker = func(time.Duration) (<-chan time.Time, func()) {
		return ticks, func() { close(stopped) }
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, time.Hour, nil); close(done) }()

	<-src.load // immediate load, before any tick
	src.set(snapOf("a", "b"), nil, "r2")
	ticks <- time.Time{}
	<-src.load
	// Run accepts this tick only after the previous Poll finished.
	ticks <- time.Time{}
	if got := s.Status().Revision; got != "r2" {
		t.Fatalf("revision = %q", got)
	}
	if s.Current() == nil || len(s.Current().Articles()) != 2 {
		t.Fatal("tick must swap in the new Snapshot")
	}

	cancel()
	<-done
	<-stopped
}

func TestSyncerRunWithoutIntervalPollsOnce(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		s := NewSyncer(plainSource{})
		s.Run(context.Background(), d, nil) // must return, not panic or block
		if s.Current() == nil {
			t.Fatalf("interval %v: Run must poll once", d)
		}
	}
}

func TestRedactLeavesNoCredentialsOrAddresses(t *testing.T) {
	for _, tc := range []struct{ msg, leak string }{
		{"Authorization: Bearer SECRET0 rejected", "SECRET0"},
		{"api_key=SECRET1 failed", "SECRET1"},
		{"client_secret=SECRET2", "SECRET2"},
		{"GOLDIE_TOKEN=SECRET3", "SECRET3"},
		{"access_token: SECRET4", "SECRET4"},
		{"x-api-key: SECRET5", "SECRET5"},
		{`password="two SECRET6"`, "SECRET6"},
		{"using ghp_SECRET7abcdefghij0123456789abcd", "SECRET7"},
		{"auth with Zm9vYmFyU0VDUkVUOGJhenF1eHF1dXgxMjM0NTY3", "Zm9vYmFy"},
		{"dial tcp 10.1.2.3:443: connect: connection refused", "10.1.2.3"},
		{"dial tcp [fd00::1]:22: i/o timeout", "fd00::1"},
		{"dial tcp fd00::1: refused", "fd00::1"},
		{"lookup git.internal.corp: no such host", "internal.corp"},
		{"connect localhost:8080 refused", "localhost:8080"},
		{"read C:\\Users\\x\\file", "Users"},
	} {
		if got := redact(tc.msg); strings.Contains(got, tc.leak) {
			t.Errorf("redact(%q) = %q leaks %q", tc.msg, got, tc.leak)
		}
	}
	if got := redact("boom"); got != "boom" {
		t.Errorf("redact(%q) = %q; a plain message must pass", "boom", got)
	}
}

// panicSource serves good once, then panics with a secret-bearing value.
type panicSource struct{ calls int }

func (p *panicSource) Load(context.Context) (*Snapshot, error) {
	p.calls++
	if p.calls == 1 {
		return snapOf("a"), nil
	}
	panic("open /srv/secret token=abc123")
}

func TestSyncerPanicInLoadKeepsLastGood(t *testing.T) {
	s := NewSyncer(&panicSource{})
	if err := s.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	good := s.Current()
	if err := s.Poll(t.Context()); err == nil {
		t.Fatal("a panicking load must fail the poll")
	}
	if s.Current() != good {
		t.Fatal("a panicking load must keep the last good Snapshot")
	}
	if got := s.Status().LastError; got != "source load panicked" {
		t.Fatalf("LastError = %q", got)
	}
}

// orderSource numbers its loads; the first one blocks until release is closed.
type orderSource struct {
	mu      sync.Mutex
	n       int
	started chan int
	release chan struct{}
}

func (o *orderSource) Load(context.Context) (*Snapshot, error) {
	o.mu.Lock()
	o.n++
	n := o.n
	o.mu.Unlock()
	o.started <- n
	if n == 1 {
		<-o.release
	}
	return snapOf(string(rune('0' + n))), nil
}

func TestSyncerConcurrentPollsNeverRegress(t *testing.T) {
	src := &orderSource{started: make(chan int, 2), release: make(chan struct{})}
	s := NewSyncer(src)

	first := make(chan struct{})
	go func() { _ = s.Poll(t.Context()); close(first) }()
	<-src.started // the first, older load is in flight

	second := make(chan struct{})
	go func() { _ = s.Poll(t.Context()); close(second) }()
	// Give an unserialised second poll the chance to finish first. A correct
	// Syncer holds it back, so this wait can only hide the bug, never fail.
	select {
	case <-second:
	case <-time.After(100 * time.Millisecond):
	}
	close(src.release)
	<-first
	<-second

	if got := s.Current().Articles()[0].ID; got != "2" {
		t.Fatalf("Current is from load %s; the newer load 2 must win", got)
	}
}

func TestRedactSchemelessUserinfo(t *testing.T) {
	for _, tc := range []struct{ msg, leak string }{
		{"auth failed for alice:hunter2@git.internal", "hunter2"},
		{"push to bob:swordfish@host rejected", "swordfish"},
		{"user alice@example.com denied", "alice"},
	} {
		if got := redact(tc.msg); strings.Contains(got, tc.leak) {
			t.Errorf("redact(%q) = %q leaks %q", tc.msg, got, tc.leak)
		}
	}
}

// A word with @ inside a quoted credential must not cut the quote short.
func TestRedactQuotedCredentialWithAt(t *testing.T) {
	msg := `password='correct horse battery@staple' rejected`
	got := redact(msg)
	for _, leak := range []string{"correct", "horse", "battery", "staple"} {
		if strings.Contains(got, leak) {
			t.Errorf("redact(%q) = %q leaks %q", msg, got, leak)
		}
	}
}

// #6: redact is narrowed to URLs, credentials and absolute paths. Each Source
// returns safe errors itself (a file relative to its root), so the Syncer's
// last line of defence must not eat what a platform engineer needs to read.
func TestRedactNarrowedLeakAndOverRedactionCases(t *testing.T) {
	leaks := map[string]struct{ msg, leak string }{
		"short name pw":            {"login failed pw=SECRETa1", "SECRETa1"},
		"short name pass":          {"login failed pass: SECRETa2", "SECRETa2"},
		"short name tok":           {"login failed tok=SECRETa3", "SECRETa3"},
		"short name auth":          {"auth=SECRETa4 rejected", "SECRETa4"},
		"gho prefix":               {"using gho_SECRETb1 failed", "SECRETb1"},
		"ghs prefix":               {"using ghs_SECRETb2 failed", "SECRETb2"},
		"github_pat with parts":    {"using github_pat_11AAA_SECRETb3part failed", "SECRETb3part"},
		"glpat with dashes":        {"using glpat-AAAA-SECRETb4 failed", "SECRETb4"},
		"unterminated double":      {`password="SECRETc1 and more words`, "more words"},
		"unterminated single":      {`token='SECRETc2 and more words`, "more words"},
		"unterminated first word":  {`secret="SECRETc3 and more`, "SECRETc3"},
		"dotless host with port":   {"dial tcp gitserver:9418: connect: connection refused", "gitserver"},
		"dotless host in lookup":   {"lookup gitserver on 127.0.0.11:53: no such host", "gitserver"},
		"dotless scp-like host":    {"fetch git@gitserver:org/repo.git failed", "gitserver"},
		"https URL":                {"fetch https://git.example.com/org/repo.git failed", "git.example.com"},
		"URL userinfo":             {"fetch https://bot:SECRETd1@git.example.com/r.git failed", "SECRETd1"},
		"absolute path":            {"open /srv/goldie/content/global/articles/a.md: no such file", "/srv/goldie"},
		"absolute path with space": {"open /var/lib/goldie/cache: permission denied", "/var/lib"},
		"windows path":             {`read C:\Users\x\file`, "Users"},
		"dotted host":              {"lookup git.internal.corp: no such host", "internal.corp"},
		"short AKIA key":           {"using AKIAIOSFODNN7EX failed", "AKIAIOSFODNN7EX"},
		"short xoxb token":         {"using xoxb-123-SECRETf2 failed", "SECRETf2"},
		"short sk key":             {"using sk-SECRETf3abc failed", "SECRETf3abc"},
		"short name sid":           {"sid=SECRETf4 rejected", "SECRETf4"},
		"json quoted name":         {`{"password": "SECRETf5"}`, "SECRETf5"},
		"compound privatekey":      {"privatekey=SECRETf6 rejected", "SECRETf6"},
		"compound clientkey":       {"clientkey: SECRETf7 rejected", "SECRETf7"},
		"compound basicauth":       {"basicauth=SECRETf8 rejected", "SECRETf8"},
		"path after colon":         {"read error:/srv/goldie/x", "/srv/goldie"},
		"path in backticks":        {"open `/srv/goldie/x` failed", "/srv/goldie"},
		"home path":                {"open ~/.ssh/id_rsa failed", ".ssh"},
		"dotless unresolved host":  {"ssh: Could not resolve hostname gitserver: Name or service not known", "gitserver"},
		"ipv4 with port":           {"dial tcp 10.1.2.3:443: connection refused", "10.1.2.3"},
	}
	for name, tc := range leaks {
		t.Run("leak/"+name, func(t *testing.T) {
			if got := redact(tc.msg); strings.Contains(got, tc.leak) {
				t.Errorf("redact(%q) = %q leaks %q", tc.msg, got, tc.leak)
			}
		})
	}

	keep := map[string]string{
		"keyword":                    "unknown keyword in front matter",
		"keywords":                   "keywords must be a list of strings",
		"version string":             "goldie v1.2.3 is older than the required 1.4.0",
		"prerelease version":         "minimum client version 1.4.0-rc.2 not met",
		"root-relative path":         "global/articles/sub/bad.md: front matter is not closed",
		"root-relative project path": "projects/github.com/acme/shop/project.yaml: suppress is not a list",
		"plain message":              "boom",
	}
	for name, msg := range keep {
		t.Run("keep/"+name, func(t *testing.T) {
			if got := redact(msg); got != msg {
				t.Errorf("redact(%q) = %q, want it unchanged", msg, got)
			}
		})
	}

	// A leaked value is removed while the useful part of the message stays.
	got := redact("global/articles/a.md: cannot open /srv/goldie/x with token=SECRETe1")
	if !strings.Contains(got, "global/articles/a.md") || strings.Contains(got, "/srv/goldie") || strings.Contains(got, "SECRETe1") {
		t.Errorf("redact kept or dropped the wrong parts: %q", got)
	}
}
