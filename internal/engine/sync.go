package engine

import (
	"context"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

// Source produces a Snapshot. DirSource is one; a git Source is another. A
// Source may also implement Revisioner.
type Source interface {
	Load() (*Snapshot, error)
}

// Revisioner is implemented by a Source that can name the revision its last
// successful Load read. The value is opaque to the engine. DirSource does not
// implement it, so its Revision is empty.
type Revisioner interface {
	Revision() string
}

// SyncStatus reports how the Syncer is doing. LastError is redacted: it holds
// no paths, URLs or credentials, and is empty after a successful poll.
type SyncStatus struct {
	Revision    string
	LastPoll    time.Time
	LastSuccess time.Time
	LastError   string
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

	// now and newTicker are replaced in tests, so no test waits on real time.
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
	return s.status
}

// Poll loads the Source once. On success it swaps the Snapshot atomically; on
// failure it keeps the previous Snapshot, records a redacted error and returns
// the original error.
func (s *Syncer) Poll() error {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	snap, err := s.src.Load()
	if err == nil && snap == nil {
		err = errNilSnapshot
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.status.LastPoll = now
	if err != nil {
		s.status.LastError = redact(err.Error())
		return err
	}
	s.current.Store(snap)
	s.status.LastSuccess = now
	s.status.LastError = ""
	if r, ok := s.src.(Revisioner); ok {
		s.status.Revision = r.Revision()
	}
	return nil
}

// Run polls once at once, then on every tick of interval, and returns when ctx
// ends. Failed polls are recorded in Status and do not stop Run.
func (s *Syncer) Run(ctx context.Context, interval time.Duration) {
	ticks, stop := s.newTicker(interval)
	defer stop()
	_ = s.Poll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			_ = s.Poll()
		}
	}
}

type syncError string

func (e syncError) Error() string { return string(e) }

const errNilSnapshot = syncError("source returned no snapshot")

var (
	urlRe  = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://\S+`)
	credRe = regexp.MustCompile(`(?i)\b(token|password|passwd|secret|key|authorization|bearer)\b\s*[:=]?\s*\S+`)
	pathRe = regexp.MustCompile(`[^\s"'` + "`" + `]*[/\\][^\s"'` + "`" + `]*`)
)

// redact strips URLs, credential-looking values and path-like words from a
// message, so a Source error is safe to show on a status endpoint.
func redact(msg string) string {
	msg = urlRe.ReplaceAllString(msg, "[redacted]")
	msg = credRe.ReplaceAllString(msg, "[redacted]")
	msg = pathRe.ReplaceAllString(msg, "[redacted]")
	return msg
}
