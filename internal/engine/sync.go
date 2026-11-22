package engine

import (
	"context"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

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
	return s.status
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
		return s.status, err
	}
	s.current.Store(snap)
	s.status.LastSuccess = now
	s.status.LastError = ""
	s.status.Revision = rev
	return s.status, nil
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

// The patterns run in order and err on the side of redacting too much: a
// redacted word costs a little detail, a leaked one may cost a credential.
var redactions = []*regexp.Regexp{
	// URLs, userinfo included.
	regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://\S+`),
	// A credential-like name (also inside an identifier such as api_key or
	// GOLDIE_TOKEN), an optional auth scheme, and the value, quoted or not.
	regexp.MustCompile(`(?i)[\w.-]*(token|passw(or)?d|pwd|secret|key|authorization|bearer|credential|cookie|session)[\w.-]*\s*[:=]?\s*((bearer|basic|token)\s+)?("[^"]*"|'[^']*'|\S+)`),
	// Userinfo without a scheme (user:password@host) and email addresses.
	// It runs after the credential pattern so it cannot swallow the closing
	// quote of a quoted value.
	regexp.MustCompile(`\S+@\S+`),
	// Bare tokens: known prefixes, and any long unbroken run.
	regexp.MustCompile(`\b(gh[pousr]_|github_pat_|glpat-)\w+`),
	regexp.MustCompile(`[A-Za-z0-9_+=-]{32,}`),
	// Path-like words.
	regexp.MustCompile(`[^\s"'` + "`" + `]*[/\\][^\s"'` + "`" + `]*`),
	// Addresses: bracketed or bare IPv6, then dotted names and IPv4, then
	// host:port.
	regexp.MustCompile(`\[[^\]\s]*\](:\d+)?`),
	regexp.MustCompile(`(?i)\b[0-9a-f]{0,4}(:[0-9a-f]{0,4}){2,7}\b`),
	regexp.MustCompile(`[\w-]+(\.[\w-]+)+(:\d+)?`),
	regexp.MustCompile(`[\w-]+:\d+\b`),
}

// redact strips URLs, credential-looking values, bare tokens, path-like words
// and network addresses from a message, so a Source error is safe to show on a
// status endpoint.
func redact(msg string) string {
	for _, re := range redactions {
		msg = re.ReplaceAllString(msg, "[redacted]")
	}
	return msg
}
