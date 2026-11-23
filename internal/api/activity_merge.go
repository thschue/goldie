package api

import (
	"context"
	"slices"
	"sync"
	"time"
)

const (
	// DefaultReorderWindow is how long the Merger holds a live event so that
	// older events from other replicas can still overtake it.
	DefaultReorderWindow = 2 * time.Second
	// DefaultMergeBuffer is the default cap on events held in the window.
	DefaultMergeBuffer = 1024

	// seenRetentionWindows and seenPerBuffered bound the dedup set. A key is
	// kept for seenRetentionWindows windows after its event was received (so
	// it outlives the event's time in the window and covers every event
	// emitted within the window), and the set never holds more than
	// seenPerBuffered times MaxBuffered keys, or twice the merged history if
	// that is larger. The oldest keys go first.
	seenRetentionWindows = 4
	seenPerBuffered      = 64
)

// MergeClock is the Merger's only source of time and timers. NewTimer's
// channel receives once when d has elapsed; d <= 0 fires at once; stop
// releases the timer.
type MergeClock interface {
	Now() time.Time
	NewTimer(d time.Duration) (fired <-chan time.Time, stop func())
}

type realMergeClock struct{}

func (realMergeClock) Now() time.Time { return time.Now() }

func (realMergeClock) NewTimer(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(max(d, 0))
	return t.C, func() { t.Stop() }
}

// MergeConfig configures a Merger. Zero values mean the defaults.
type MergeConfig struct {
	Window       time.Duration // reorder window; <= 0 means DefaultReorderWindow
	MaxBuffered  int           // hard cap on events held in the window; <= 0 means DefaultMergeBuffer
	OutputBuffer int           // capacity of the output channel; <= 0 means ActivitySubscriberBuffer
	Clock        MergeClock    // nil means the real clock
}

// MergeInput is one replica's feed. Name identifies it in status items and is
// the only replica name its events may carry. It is the peer's replica
// identity as the operator configured it: never the peer's address and never
// a name the peer reports about itself. History is that replica's retained events; Live is its live stream, and a
// nil Live means the replica is unreachable.
type MergeInput struct {
	Name    string
	History []Event
	Live    <-chan Event
}

// ReplicaStatus reports an input going down or coming back up. LastSeen is the
// merger clock time of the last event received from it, else when it was
// attached.
type ReplicaStatus struct {
	Replica  string
	Up       bool
	LastSeen time.Time
}

// MergedItem is one output item; exactly one field is non-nil.
type MergedItem struct {
	Event  *Event
	Status *ReplicaStatus
}

type eventKey struct {
	replica string
	id      uint64
}

// seenEntry is one key in the order it was seen; seq tells it apart from the
// same key seen again after Add forgot it.
type seenEntry struct {
	key eventKey
	at  time.Time
	seq uint64
}

type heldEvent struct {
	e   Event
	due time.Time
}

// mergeInputState is what the Merger keeps per input name. The high-water
// mark is the largest Time and ID taken from this input for events of its own
// replica (Event.Replica == the input's Name). It makes a re-attached
// History exact however long the outage was, in O(1) memory per input: an
// event replayed through Add is skipped when its ID and its Time are both at
// or below the mark. A peer that restarted and reset its IDs is told apart by
// Time: its new events are newer than the mark, so they pass even with small
// IDs. The mark trusts the input's own timestamps, so a peer that sends a far
// future Time only hides its own older replays, never another replica's.
//
// The mark has two limits. Live events the Merger dropped (a full output or
// buffer) are not recovered when the input re-attaches, because the mark
// already moved past them. And a peer that restarts with reset IDs while its
// clock lags behind the mark loses that one replay: its new events are at or
// below the mark in both ID and Time, so they are skipped as already taken.
type mergeInputState struct {
	up       bool
	lastSeen time.Time

	marked   bool
	markTime time.Time
	markID   uint64
}

func (s *mergeInputState) atOrBelowMark(e Event) bool {
	return s.marked && e.ID <= s.markID && !e.Time.After(s.markTime)
}

func (s *mergeInputState) raiseMark(e Event) {
	if !s.marked || e.Time.After(s.markTime) {
		s.markTime = e.Time
	}
	if !s.marked || e.ID > s.markID {
		s.markID = e.ID
	}
	s.marked = true
}

// normalizeEvent is the one check every event passes, live or history: it
// rejects an empty Replica or ID 0, cuts every string field a peer controls
// to maxQueryBytes (boundEvent) and clears Late, which only the Merger sets.
func normalizeEvent(e Event) (Event, bool) {
	if e.Replica == "" || e.ID == 0 {
		return Event{}, false
	}
	e = boundEvent(e)
	e.Late = false
	return e, true
}

// compareEvents orders by Time, then Replica, then ID.
func compareEvents(a, b Event) int {
	if c := a.Time.Compare(b.Time); c != 0 {
		return c
	}
	if a.Replica != b.Replica {
		if a.Replica < b.Replica {
			return -1
		}
		return 1
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	}
	return 0
}

// mergeHistories merges the inputs' histories sorted by Time, then Replica,
// then ID, keeping the first copy of each Replica+ID. Events pass the same
// checks as live ones: normalizeEvent, and Replica must equal the input's
// Name. Events that fail are left out and counted in rejected, once. It does
// not modify its arguments.
func mergeHistories(inputs []MergeInput) (out []Event, rejected uint64) {
	seen := map[eventKey]struct{}{}
	for _, in := range inputs {
		for _, e := range in.History {
			e, ok := normalizeEvent(e)
			if !ok || e.Replica != in.Name {
				rejected++
				continue
			}
			k := eventKey{e.Replica, e.ID}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, compareEvents)
	return out, rejected
}

// Merger merges the activity of several replicas into one stream. Live events
// wait in a reorder window so older ones from other replicas can overtake
// them, duplicates are dropped by Replica+ID, and an event that arrives after
// its window leaves at once marked Late. It never blocks on its output (a full
// output drops and counts), a stalled input never delays the others, and it
// never logs event or query text. Its methods are safe for concurrent use.
type Merger struct {
	window time.Duration
	maxBuf int
	clock  MergeClock
	out    chan MergedItem
	done   chan struct{}
	kick   chan struct{}

	mu       sync.Mutex
	wg       sync.WaitGroup
	ctx      context.Context
	started  bool
	stopped  bool
	inputs   map[string]*mergeInputState
	held     []heldEvent         // sorted by event key
	seen     map[eventKey]uint64 // the seq of the key's newest seenFIFO entry
	seenFIFO []seenEntry
	seenHead int
	seenMax  int
	seenSeq  uint64

	dropped, duplicates, rejected uint64
}

// NewMerger returns a Merger; call Start once.
func NewMerger(cfg MergeConfig) *Merger {
	if cfg.Window <= 0 {
		cfg.Window = DefaultReorderWindow
	}
	if cfg.MaxBuffered <= 0 {
		cfg.MaxBuffered = DefaultMergeBuffer
	}
	if cfg.OutputBuffer <= 0 {
		cfg.OutputBuffer = ActivitySubscriberBuffer
	}
	if cfg.Clock == nil {
		cfg.Clock = realMergeClock{}
	}
	return &Merger{
		window: cfg.Window, maxBuf: cfg.MaxBuffered, clock: cfg.Clock,
		out:  make(chan MergedItem, cfg.OutputBuffer),
		done: make(chan struct{}), kick: make(chan struct{}, 1),
		inputs: map[string]*mergeInputState{}, seen: map[eventKey]uint64{},
		seenMax: seenPerBuffered * cfg.MaxBuffered,
	}
}

// Start attaches the inputs and returns the merged history of all of them
// (not replayed on the output) and the output channel. Cancelling ctx stops
// every goroutine, closes the output, then closes Done. Start works once; a
// second call returns no history and the same output.
func (m *Merger) Start(ctx context.Context, inputs ...MergeInput) ([]Event, <-chan MergedItem) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil, m.out
	}
	m.started = true
	m.ctx = ctx
	// A second input with a name already given is ignored whole (its History
	// and its Live, which is never read) and counted once in Rejected: the
	// first one wins.
	var kept []MergeInput
	names := map[string]bool{}
	for _, in := range inputs {
		if names[in.Name] {
			m.rejected++
			continue
		}
		names[in.Name] = true
		kept = append(kept, in)
	}
	history, rejected := mergeHistories(kept)
	m.rejected += rejected
	m.seenMax = max(m.seenMax, 2*len(history))
	now := m.clock.Now()
	for _, e := range history {
		m.markSeen(eventKey{e.Replica, e.ID}, now)
	}
	for _, in := range kept {
		hist := in.History
		in.History = nil // already merged
		m.attachLocked(in, now, false)
		for _, e := range hist {
			if e, ok := normalizeEvent(e); ok && e.Replica == in.Name {
				m.inputs[in.Name].raiseMark(e)
			}
		}
	}
	m.wg.Add(1)
	go m.schedule()
	m.mu.Unlock()

	go func() {
		<-ctx.Done()
		m.mu.Lock()
		m.stopped = true
		m.mu.Unlock()
		m.wg.Wait()
		close(m.out)
		close(m.done)
	}()
	return history, m.out
}

// Add attaches an input, or re-attaches one that is down (one up status).
// Its History is treated as live events (deduped, late if old), except that
// events at or below the input's high-water mark are skipped as already taken,
// however long ago. Only the mark decides that for a re-attached input: its
// keys are dropped from the dedup set, so a peer that restarted with ids
// from 1 again is not taken for its old self. Its replayed old events are
// still skipped by the mark, and its keys seen after the re-attach dedup as
// usual. A brand-new
// name emits no up status: up is the initial state. It returns false, changing
// nothing,
// for an empty Name, an input that is up, a down input given no Live, or a
// stopped Merger.
func (m *Merger) Add(in MergeInput) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started || m.stopped || in.Name == "" || m.ctx.Err() != nil {
		return false
	}
	st, ok := m.inputs[in.Name]
	if ok && (st.up || in.Live == nil) {
		return false
	}
	if ok {
		for k := range m.seen {
			if k.replica == in.Name {
				delete(m.seen, k)
			}
		}
	}
	m.attachLocked(in, m.clock.Now(), true)
	return true
}

// Done is closed once the output is closed and every goroutine has returned.
func (m *Merger) Done() <-chan struct{} { return m.done }

// Buffered is the number of events held in the window now.
func (m *Merger) Buffered() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.held)
}

// Dropped counts items the full output refused.
func (m *Merger) Dropped() uint64 { return m.counter(&m.dropped) }

// Duplicates counts events discarded as already seen.
func (m *Merger) Duplicates() uint64 { return m.counter(&m.duplicates) }

// Rejected counts events discarded as invalid (empty Replica or ID 0), events
// whose Replica differs from their input's Name, and inputs ignored because an
// earlier one already had their name (each counted once).
func (m *Merger) Rejected() uint64 { return m.counter(&m.rejected) }

func (m *Merger) counter(c *uint64) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *c
}

// attachLocked registers in at now. The caller holds mu and has checked that
// the name is new or down.
func (m *Merger) attachLocked(in MergeInput, now time.Time, replay bool) {
	st, existed := m.inputs[in.Name]
	if !existed {
		st = &mergeInputState{}
		m.inputs[in.Name] = st
	}
	st.lastSeen = now
	if in.Live == nil {
		st.up = false
		m.emitLocked(MergedItem{Status: &ReplicaStatus{Replica: in.Name, LastSeen: now}})
	} else {
		st.up = true
		if existed {
			m.emitLocked(MergedItem{Status: &ReplicaStatus{Replica: in.Name, Up: true, LastSeen: now}})
		}
		m.wg.Add(1)
		go m.read(in.Name, in.Live)
	}
	for _, e := range in.History {
		m.receiveLocked(in.Name, e, now, replay)
	}
}

// read forwards one input until it closes or the Merger stops.
func (m *Merger) read(name string, live <-chan Event) {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case e, ok := <-live:
			m.mu.Lock()
			now := m.clock.Now()
			st := m.inputs[name]
			if !ok {
				st.up = false
				m.emitLocked(MergedItem{Status: &ReplicaStatus{Replica: name, LastSeen: st.lastSeen}})
				m.mu.Unlock()
				return
			}
			st.lastSeen = now
			m.receiveLocked(name, e, now, false)
			m.mu.Unlock()
		}
	}
}

// receiveLocked takes one event received at now: reject, dedup, then emit it
// late, or hold it (pushing the oldest held event out first at the cap). The
// per-event cost is O(n) in the held set (slice insert), bounded by MaxBuffered.
// replay marks an event from a re-attached History, checked against from's
// high-water mark first.
func (m *Merger) receiveLocked(from string, e Event, now time.Time, replay bool) {
	e, ok := normalizeEvent(e)
	if !ok {
		m.rejected++
		return
	}
	if e.Replica != from {
		m.rejected++
		return
	}
	st := m.inputs[from]
	if replay && st.atOrBelowMark(e) {
		m.duplicates++
		return
	}
	st.raiseMark(e)
	k := eventKey{e.Replica, e.ID}
	if _, dup := m.seen[k]; dup {
		m.duplicates++
		return
	}
	m.markSeen(k, now)
	if now.Sub(e.Time) > m.window {
		e.Late = true
		m.emitLocked(MergedItem{Event: &e})
		return
	}
	if len(m.held) >= m.maxBuf {
		oldest := m.held[0].e
		m.held = slices.Delete(m.held, 0, 1)
		oldest.Late = true
		m.emitLocked(MergedItem{Event: &oldest})
	}
	h := heldEvent{e: e, due: minTime(e.Time, now).Add(m.window)}
	i, _ := slices.BinarySearchFunc(m.held, h, func(a, b heldEvent) int { return compareEvents(a.e, b.e) })
	m.held = slices.Insert(m.held, i, h)
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// markSeen remembers k and evicts keys that are too old or over the bound.
// The bound is on the queue, not the set: Add forgets keys from seen but
// leaves their entries queued, and every key in seen has an entry, so a
// bounded queue bounds both.
func (m *Merger) markSeen(k eventKey, now time.Time) {
	m.seenSeq++
	m.seen[k] = m.seenSeq
	m.seenFIFO = append(m.seenFIFO, seenEntry{k, now, m.seenSeq})
	for m.seenHead < len(m.seenFIFO) {
		old := m.seenFIFO[m.seenHead]
		if len(m.seenFIFO)-m.seenHead <= m.seenMax && now.Sub(old.at) <= seenRetentionWindows*m.window {
			break
		}
		if m.seen[old.key] == old.seq { // not the same key seen again since Add forgot it
			delete(m.seen, old.key)
		}
		m.seenHead++
	}
	if m.seenHead > 1024 && m.seenHead > len(m.seenFIFO)/2 {
		m.seenFIFO = slices.Clone(m.seenFIFO[m.seenHead:])
		m.seenHead = 0
	}
}

// emitLocked offers it to the output without waiting.
func (m *Merger) emitLocked(it MergedItem) {
	select {
	case m.out <- it:
	default:
		m.dropped++
	}
}

// releaseLocked emits every held event that is due at now, in key order, and
// returns the earliest due time of those left (zero when none).
func (m *Merger) releaseLocked(now time.Time) (next time.Time) {
	kept := m.held[:0]
	for _, h := range m.held {
		if h.due.After(now) {
			kept = append(kept, h)
			if next.IsZero() || h.due.Before(next) {
				next = h.due
			}
			continue
		}
		e := h.e
		m.emitLocked(MergedItem{Event: &e})
	}
	clear(m.held[len(kept):])
	m.held = kept
	return next
}

// schedule releases held events as they fall due. It trusts only the clock's
// Now, never a timer value, so a timer firing early or late is harmless.
func (m *Merger) schedule() {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		next := m.releaseLocked(m.clock.Now())
		m.mu.Unlock()

		var fired <-chan time.Time
		stop := func() {}
		if !next.IsZero() {
			var due bool
			if fired, stop, due = m.arm(next); due {
				continue
			}
		}
		select {
		case <-m.ctx.Done():
			stop()
			return
		case <-fired:
		case <-m.kick:
		}
		stop()
	}
}

// arm starts a timer for the instant next. A timer counts from the moment it
// is created, so if the clock moved between reading Now and creating it, the
// timer would be late by that much. arm therefore reads Now again afterwards
// and, if it moved, re-arms from the new reading (a few times at most, so a
// real clock that always moves a little cannot spin). due reports that next
// has already passed, with no timer left running.
func (m *Merger) arm(next time.Time) (fired <-chan time.Time, stop func(), due bool) {
	for try := 0; ; try++ {
		before := m.clock.Now()
		fired, stop = m.clock.NewTimer(next.Sub(before))
		after := m.clock.Now()
		if !after.Before(next) {
			stop()
			return nil, func() {}, true
		}
		if after.Equal(before) || try >= 8 {
			return fired, stop, false
		}
		stop()
	}
}
