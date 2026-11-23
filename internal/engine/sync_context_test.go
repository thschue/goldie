package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// recv waits a bounded time for a value on ch, so a missing poll, report or
// return fails the test that expects it instead of hanging the package.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// blockingSource counts its loads and blocks every one until release is
// closed, whatever its context says. A Load that starts at all is visible in
// calls, and a Run that calls it cannot return before release.
type blockingSource struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func (b *blockingSource) Load(context.Context) (*Snapshot, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	<-b.release
	return snapOf("late"), nil
}

func (b *blockingSource) loads() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// TestSyncerRunWithCancelledContextNeverLoads cancels the context before Run's
// first poll, with a tick already waiting and a Source that would block: Run
// returns at once and Load is never called, with and without an interval.
func TestSyncerRunWithCancelledContextNeverLoads(t *testing.T) {
	for _, interval := range []time.Duration{time.Hour, 0} {
		t.Run(interval.String(), func(t *testing.T) {
			src := &blockingSource{release: make(chan struct{})}
			t.Cleanup(func() { close(src.release) })
			s := NewSyncer(src)
			ticks := make(chan time.Time, 1)
			ticks <- time.Time{}
			s.newTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
			polled := 0
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			done := make(chan struct{})
			go func() { s.Run(ctx, interval, func(SyncStatus) { polled++ }); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("Run did not return after cancel; Load was called %d times", src.loads())
			}
			if n := src.loads(); n != 0 {
				t.Fatalf("Load called %d times with the context already cancelled", n)
			}
			if polled != 0 {
				t.Fatalf("the poll callback ran %d times, but no poll happened", polled)
			}
			if s.Current() != nil || !s.Status().LastPoll.IsZero() {
				t.Fatalf("a cancelled Run recorded a poll: %+v", s.Status())
			}
		})
	}
}

// TestSyncerRunChecksContextBeforeEachTick cancels the context while a tick is
// waiting. Run's select may take either; whichever it takes, no further Load
// runs. Repeated, so both choices of the select are taken.
func TestSyncerRunChecksContextBeforeEachTick(t *testing.T) {
	for i := range 50 {
		src := &fakeSource{load: make(chan struct{}, 8)}
		src.set(snapOf("a"), nil, "r")
		s := NewSyncer(src)
		ticks := make(chan time.Time, 1)
		s.newTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
		ctx, cancel := context.WithCancel(context.Background())
		var mu sync.Mutex
		polls := 0
		polled := make(chan struct{}, 8)
		done := make(chan struct{})
		go func() {
			s.Run(ctx, time.Hour, func(SyncStatus) {
				mu.Lock()
				polls++
				mu.Unlock()
				polled <- struct{}{}
			})
			close(done)
		}()

		recv(t, polled, "the report of the first poll") // the first poll is over; Run is at its select
		cancel()
		ticks <- time.Time{}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after cancel")
		}
		mu.Lock()
		n := polls
		mu.Unlock()
		if loads := len(src.load); n != 1 || loads != 1 {
			t.Fatalf("round %d: %d polls and %d further loads after cancel; a tick after cancel must not poll", i, n-1, loads-1)
		}
	}
}

// ctxSource serves good loads until fail is set; then each Load waits for its
// context to end and returns the context's error, as a cancelled fetch does.
type ctxSource struct {
	mu      sync.Mutex
	fail    bool
	started chan struct{}
}

func (c *ctxSource) Load(ctx context.Context) (*Snapshot, error) {
	c.mu.Lock()
	fail := c.fail
	c.mu.Unlock()
	if !fail {
		return snapOf("good"), nil
	}
	if c.started != nil {
		c.started <- struct{}{}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestSyncerPollPassesItsContextToLoad cancels the context given to Poll: the
// Load sees it, and the failed poll keeps the last good Snapshot.
func TestSyncerPollPassesItsContextToLoad(t *testing.T) {
	src := &ctxSource{}
	s := NewSyncer(src)
	if err := s.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	good := s.Current()

	src.mu.Lock()
	src.fail = true
	src.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- s.Poll(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a poll whose context was cancelled succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Poll did not hand its context to Load: the Load never saw the cancel")
	}
	if s.Current() != good {
		t.Fatal("a cancelled poll must keep the last good Snapshot")
	}
	if s.Status().LastError == "" {
		t.Fatal("a cancelled poll must record an error")
	}
}

// TestSyncerRunCancelStopsAnInFlightLoad cancels Run's context while a Load is
// in flight: the Load gets Run's context, so it stops and Run returns, and the
// last good Snapshot keeps serving.
func TestSyncerRunCancelStopsAnInFlightLoad(t *testing.T) {
	src := &ctxSource{started: make(chan struct{}, 1)}
	s := NewSyncer(src)
	ticks := make(chan time.Time)
	s.newTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
	polled := make(chan SyncStatus, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx, time.Hour, func(st SyncStatus) { polled <- st }); close(done) }()

	recv(t, polled, "the report of the first poll")
	good := s.Current()
	if good == nil {
		t.Fatal("first poll loaded nothing")
	}
	src.mu.Lock()
	src.fail = true
	src.mu.Unlock()
	ticks <- time.Time{}
	recv(t, src.started, "the second Load") // the second Load is in flight
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return: cancelling its context did not stop the Load in flight")
	}
	if s.Current() != good {
		t.Fatal("a load stopped by cancel must keep the last good Snapshot")
	}
}

// TestSyncerRunReportsEachPoll gives Run a callback: it runs once after every
// poll, successful or not, with the Status that poll left, redacted.
func TestSyncerRunReportsEachPoll(t *testing.T) {
	src := &fakeSource{}
	src.set(snapOf("a"), nil, "rev1")
	s := NewSyncer(src)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	s.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func() { clockMu.Lock(); clock = clock.Add(time.Minute); clockMu.Unlock() }
	ticks := make(chan time.Time)
	s.newTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
	reports := make(chan SyncStatus, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, time.Hour, func(st SyncStatus) { reports <- st }); close(done) }()

	next := func() SyncStatus {
		t.Helper()
		select {
		case st := <-reports:
			return st
		case <-time.After(5 * time.Second):
			t.Fatal("no report after a poll")
			return SyncStatus{}
		}
	}

	first := next()
	if first.Revision != "rev1" || first.LastError != "" || first.LastSuccess.IsZero() || !first.LastPoll.Equal(first.LastSuccess) {
		t.Fatalf("report after a good poll = %+v", first)
	}

	advance()
	src.set(nil, errors.New("clone https://u:hunter2@git.internal/r.git failed: token=abc123"), "")
	ticks <- time.Time{}
	failed := next()
	if failed.LastError == "" || failed.Revision != "rev1" || !failed.LastSuccess.Equal(first.LastSuccess) || !failed.LastPoll.After(first.LastPoll) {
		t.Fatalf("report after a failed poll = %+v", failed)
	}
	for _, leak := range []string{"hunter2", "abc123", "git.internal", "https://"} {
		if strings.Contains(failed.LastError, leak) {
			t.Fatalf("reported LastError %q leaks %q", failed.LastError, leak)
		}
	}
	if failed != s.Status() {
		t.Fatalf("report %+v is not the Status the poll left, %+v", failed, s.Status())
	}

	advance()
	src.set(snapOf("a", "b"), nil, "rev2")
	ticks <- time.Time{}
	if second := next(); second.Revision != "rev2" || second.LastError != "" {
		t.Fatalf("report after recovery = %+v", second)
	}

	cancel()
	recv(t, done, "Run to return after cancel")
	if n := len(reports); n != 0 {
		t.Fatalf("%d reports without a poll", n)
	}
}

// TestSyncerRunWithoutIntervalReportsOnce: the single poll of Run without an
// interval is reported too.
func TestSyncerRunWithoutIntervalReportsOnce(t *testing.T) {
	s := NewSyncer(plainSource{})
	var got []SyncStatus
	s.Run(context.Background(), 0, func(st SyncStatus) { got = append(got, st) })
	if len(got) != 1 || got[0].LastSuccess.IsZero() {
		t.Fatalf("reports = %+v, want one successful poll", got)
	}
}

// TestSyncerFailedPollKeepsLastGoodSnapshot is the regression test: through
// Run, a failing poll, an empty result and a panicking load each keep the
// Snapshot of the last good poll, and the next good poll replaces it.
func TestSyncerFailedPollKeepsLastGoodSnapshot(t *testing.T) {
	src := &fakeSource{}
	good := snapOf("a")
	src.set(good, nil, "rev1")
	s := NewSyncer(src)
	ticks := make(chan time.Time)
	s.newTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
	reports := make(chan SyncStatus, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, time.Hour, func(st SyncStatus) { reports <- st }); close(done) }()
	recv(t, reports, "the report of the first poll")

	for _, fail := range []struct {
		name string
		snap *Snapshot
		err  error
	}{
		{"error", nil, errors.New("boom")},
		{"no snapshot", nil, nil},
		{"error with a snapshot", snapOf("partial"), errors.New("half read")},
	} {
		src.set(fail.snap, fail.err, "bad")
		ticks <- time.Time{}
		st := recv(t, reports, "the report of a failed poll")
		if s.Current() != good {
			t.Fatalf("%s: the failed poll replaced the last good Snapshot", fail.name)
		}
		if st.Revision != "rev1" || st.LastError == "" {
			t.Fatalf("%s: status = %+v", fail.name, st)
		}
	}

	next := snapOf("a", "b")
	src.set(next, nil, "rev2")
	ticks <- time.Time{}
	recv(t, reports, "the report of the good poll")
	if s.Current() != next {
		t.Fatal("a good poll after failures must swap in its Snapshot")
	}
	cancel()
	recv(t, done, "Run to return after cancel")
}

// cancelPanicSource serves good once; every later Load waits for its context
// to end and then panics with a secret-bearing value.
type cancelPanicSource struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
}

func (c *cancelPanicSource) Load(ctx context.Context) (*Snapshot, error) {
	c.mu.Lock()
	c.calls++
	n := c.calls
	c.mu.Unlock()
	if n == 1 {
		return snapOf("good"), nil
	}
	if c.started != nil {
		c.started <- struct{}{}
	}
	<-ctx.Done()
	panic("open /srv/secret token=abc123 after " + ctx.Err().Error())
}

// TestSyncerPanicInLoadWithCancelledContext: a Load that panics once its
// context is cancelled fails that poll with the fixed message, keeps the last
// good Snapshot, and does not take Run or the process down.
func TestSyncerPanicInLoadWithCancelledContext(t *testing.T) {
	t.Run("Poll", func(t *testing.T) {
		s := NewSyncer(&cancelPanicSource{})
		if err := s.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		good := s.Current()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		polled := make(chan error, 1)
		go func() { polled <- s.Poll(ctx) }()
		if err := recv(t, polled, "Poll to hand its cancelled context to Load"); err == nil || err.Error() != "source load panicked" {
			t.Fatalf("err = %v, want the fixed panic error", err)
		}
		if s.Current() != good {
			t.Fatal("a panicking load must keep the last good Snapshot")
		}
		if got := s.Status().LastError; got != "source load panicked" {
			t.Fatalf("LastError = %q", got)
		}
	})

	t.Run("Run", func(t *testing.T) {
		src := &cancelPanicSource{started: make(chan struct{}, 1)}
		s := NewSyncer(src)
		ticks := make(chan time.Time)
		s.newTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
		reports := make(chan SyncStatus, 8)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		go func() { s.Run(ctx, time.Hour, func(st SyncStatus) { reports <- st }); close(done) }()
		recv(t, reports, "the report of the first poll")
		good := s.Current()

		ticks <- time.Time{}
		recv(t, src.started, "the second Load")
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after a Load panicked on cancel")
		}
		if s.Current() != good {
			t.Fatal("a panicking load must keep the last good Snapshot")
		}
		if got := s.Status().LastError; got != "source load panicked" {
			t.Fatalf("LastError = %q", got)
		}
	})
}
