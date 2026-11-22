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

func (f *fakeSource) Load() (*Snapshot, error) {
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
	if err := s.Poll(); err == nil {
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
	if err := s.Poll(); err != nil {
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
	if err := s.Poll(); err == nil {
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
	if err := s.Poll(); err != nil {
		t.Fatal(err)
	}
	if s.Current() != second || s.Status().Revision != "rev2" {
		t.Fatal("success must swap the Snapshot and revision")
	}
}

func TestSyncerNilSnapshotIsFailure(t *testing.T) {
	s := NewSyncer(&fakeSource{})
	if err := s.Poll(); err == nil || s.Current() != nil {
		t.Fatal("a nil Snapshot without error must count as a failure")
	}
}

// plainSource has no Revision method, like DirSource.
type plainSource struct{}

func (plainSource) Load() (*Snapshot, error) { return snapOf(), nil }

func TestSyncerRevisionEmptyWithoutRevisioner(t *testing.T) {
	s := NewSyncer(plainSource{})
	if err := s.Poll(); err != nil {
		t.Fatal(err)
	}
	if r := s.Status().Revision; r != "" {
		t.Fatalf("revision = %q", r)
	}
	var _ Source = NewDirSource("x")
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
	go func() { s.Run(ctx, time.Hour); close(done) }()

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

// orderSource numbers its loads; the first one blocks until release is closed.
type orderSource struct {
	mu      sync.Mutex
	n       int
	started chan int
	release chan struct{}
}

func (o *orderSource) Load() (*Snapshot, error) {
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
	go func() { _ = s.Poll(); close(first) }()
	<-src.started // the first, older load is in flight

	second := make(chan struct{})
	go func() { _ = s.Poll(); close(second) }()
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
