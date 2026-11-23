package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// SyncStatus reports how the Syncer is doing. LastError is redacted: it holds
// no host path, URL or credential (a file is named relative to the Source root),
// and is empty after a successful poll.
// Warnings are the findings of the last successful load that did not fail it,
// such as a suppress reference that names nothing. Each names a file relative
// to the Source root, is printable ASCII and at most 1 KiB. A warning is not
// redacted: what the Source wrote (a well-formed reference) is escaped to
// ASCII and cut, not stripped.
type SyncStatus struct {
	Revision    string
	LastPoll    time.Time
	LastSuccess time.Time
	LastError   string
	Warnings    []string
}

// Syncer polls one Source and serves the latest good Snapshot. Current is safe
// for concurrent readers; a failed load never replaces the last good Snapshot.
type Syncer struct {
	src     Source
	current atomic.Pointer[Snapshot]

	// pollMu serialises whole polls, Load included, so a slow older load can
	// never replace a newer Snapshot. mu only guards status, so Status does not
	// wait for a slow Load.
	pollMu sync.Mutex
	mu     sync.Mutex
	status SyncStatus

	// now and newTicker are replaced in tests, so no test waits on a ticker.
	now       func() time.Time
	newTicker func(d time.Duration) (ticks <-chan time.Time, stop func())
}

// NewSyncer returns a Syncer for src. It loads nothing until Poll or Run.
func NewSyncer(src Source) *Syncer {
	return &Syncer{
		src: src,
		now: time.Now,
		newTicker: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		},
	}
}

// Current returns the latest good Snapshot, or nil before the first successful
// load.
func (s *Syncer) Current() *Snapshot { return s.current.Load() }

// Status returns a copy of the current status.
func (s *Syncer) Status() SyncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusCopy()
}

// statusCopy returns status with its own Warnings slice. Callers hold mu.
func (s *Syncer) statusCopy() SyncStatus {
	st := s.status
	st.Warnings = append([]string(nil), st.Warnings...)
	return st
}

// Poll loads the Source once, handing it ctx, so cancelling ctx stops the
// load. On success it swaps the Snapshot atomically; on failure it keeps the
// previous Snapshot, records a redacted error and returns the original error.
func (s *Syncer) Poll(ctx context.Context) error {
	_, err := s.poll(ctx)
	return err
}

// poll is Poll, also returning the Status it left.
func (s *Syncer) poll(ctx context.Context) (SyncStatus, error) {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	return s.pollLocked(ctx)
}

// Trigger starts one poll in the background, with ctx and then onPoll as Run
// has them, and reports whether it did. It starts none, and reports false,
// when a poll is already running or ctx has ended. The poll runs to the end of
// ctx, not of the caller's request.
func (s *Syncer) Trigger(ctx context.Context, onPoll func(SyncStatus)) bool {
	if ctx.Err() != nil || !s.pollMu.TryLock() {
		return false
	}
	go func() {
		defer s.pollMu.Unlock()
		st, _ := s.pollLocked(ctx)
		if onPoll != nil {
			onPoll(st)
		}
	}()
	return true
}

// pollLocked is poll for a caller that holds pollMu.
func (s *Syncer) pollLocked(ctx context.Context) (SyncStatus, error) {
	snap, rev, err := s.load(ctx)
	if err == nil && snap == nil {
		err = errNilSnapshot
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.status.LastPoll = now
	if err != nil {
		s.status.LastError = redact(err.Error())
		return s.statusCopy(), err
	}
	s.current.Store(snap)
	s.status.LastSuccess = now
	s.status.LastError = ""
	s.status.Revision = rev
	s.status.Warnings = append([]string(nil), snap.warnings...)
	return s.statusCopy(), nil
}

// load calls the Source. A panic in Load or Revision becomes errLoadPanicked,
// so a Source bug fails one poll instead of the process, and the panic value
// never reaches Status.
func (s *Syncer) load(ctx context.Context) (snap *Snapshot, rev string, err error) {
	defer func() {
		if recover() != nil {
			snap, rev, err = nil, "", errLoadPanicked
		}
	}()
	snap, err = s.src.Load(ctx)
	if err == nil && snap != nil {
		if r, ok := s.src.(Revisioner); ok {
			rev = r.Revision()
		}
	}
	return snap, rev, err
}

// Run polls once at once, then on every tick of interval, and returns when ctx
// ends. Each poll gets ctx, so cancelling it stops a load in flight, and ctx is
// checked before each poll, so no poll starts after cancel. Failed polls are
// recorded in Status and do not stop Run. onPoll, when not nil, gets the Status
// each poll left, after that poll; it is never called without a poll. An
// interval of zero or less means no periodic polling: Run polls once and
// returns.
func (s *Syncer) Run(ctx context.Context, interval time.Duration, onPoll func(SyncStatus)) {
	poll := func() {
		if ctx.Err() != nil {
			return
		}
		st, _ := s.poll(ctx)
		if onPoll != nil {
			onPoll(st)
		}
	}
	if interval <= 0 {
		poll()
		return
	}
	ticks, stop := s.newTicker(interval)
	defer stop()
	poll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			poll()
		}
	}
}

type syncError string

func (e syncError) Error() string { return string(e) }

const (
	errNilSnapshot  = syncError("source returned no snapshot")
	errLoadPanicked = syncError("source load panicked")
)
