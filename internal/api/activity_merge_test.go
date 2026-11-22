package api

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The contract these tests fix for the pure activity merger (#244). It has no
// HTTP; #245 feeds it one MergeInput per replica and serialises its output.
//
//	// Event (activity.go) gains one field, set only by the merger:
//	//   Late bool // true when the event reached the merger after its reorder window
//
//	const DefaultReorderWindow = 2 * time.Second
//	const DefaultMergeBuffer = 1024 // default MergeConfig.MaxBuffered
//
//	// MergeClock is the merger's only source of time and timers. A nil
//	// MergeConfig.Clock means the real clock. NewTimer's channel receives once
//	// when d has elapsed; d <= 0 fires at once; stop releases the timer.
//	type MergeClock interface {
//		Now() time.Time
//		NewTimer(d time.Duration) (fired <-chan time.Time, stop func())
//	}
//
//	type MergeConfig struct {
//		Window       time.Duration // reorder window; <= 0 means DefaultReorderWindow
//		MaxBuffered  int           // hard cap on events held in the window; <= 0 means DefaultMergeBuffer
//		OutputBuffer int           // capacity of the output channel; <= 0 means ActivitySubscriberBuffer
//		Clock        MergeClock
//	}
//
//	// One replica's feed. Name identifies the input in replica_status events.
//	// History is that replica's retained events (as Activity.Subscribe gives
//	// them); Live is its live stream. A nil Live means the replica is unreachable.
//	type MergeInput struct {
//		Name    string
//		History []Event
//		Live    <-chan Event
//	}
//
//	type ReplicaStatus struct {
//		Replica  string    // the MergeInput.Name
//		Up       bool
//		LastSeen time.Time // merger clock time of the last event received from it, else when it was attached
//	}
//
//	// Exactly one of Event and Status is non-nil.
//	type MergedItem struct {
//		Event  *Event
//		Status *ReplicaStatus
//	}
//
//	func MergeHistories(histories ...[]Event) []Event // sorted by Time, then Replica, then ID; deduped by Replica+ID, first wins
//	func NewMerger(cfg MergeConfig) *Merger
//	func (m *Merger) Start(ctx context.Context, inputs ...MergeInput) (history []Event, out <-chan MergedItem)
//	func (m *Merger) Add(in MergeInput) bool // attach or re-attach an input while running
//	func (m *Merger) Done() <-chan struct{}  // closed once out is closed and every goroutine has returned
//	func (m *Merger) Buffered() int          // events held in the window now
//	func (m *Merger) Dropped() uint64        // items the full output refused
//	func (m *Merger) Duplicates() uint64     // events discarded as already seen (Replica+ID)
//	func (m *Merger) Rejected() uint64       // events discarded as invalid (empty Replica or ID 0)
//
// Behaviour fixed here:
//   - History: Start returns MergeHistories of every input's History. Those
//     keys count as seen, so the same event repeated live is not emitted again.
//   - Window: an event received at clock time N is held until
//     min(Event.Time, N) + Window <= clock now, so a far-future timestamp waits
//     at most one window. Due events leave in (Time, Replica, ID) order.
//   - Late: an event with N - Time > Window is emitted at once with Late=true
//     (exactly one Window old is not late). Events are never dropped for
//     lateness. Events inside the window come out with Late=false.
//   - Dedup: by Replica+ID across all inputs, also after the first copy was
//     emitted. The first copy wins. Event.ID going backwards is accepted when
//     the key is new.
//   - Invalid events (empty Replica, ID 0) are discarded and counted in Rejected.
//   - Availability: an input is down when its Live channel is closed or was nil
//     at attach; silence alone never marks it down (an idle replica is
//     normal; the transport closes Live on a timeout). A down status is
//     emitted at once, not held for the window, and events the input sent
//     before it closed stay buffered and are emitted normally. Add with the
//     name of a down input brings it up: one up status, LastSeen = now. Add
//     with the name of an input that is up, or with an empty Name, returns
//     false and changes nothing. A brand-new Name attached with a live channel
//     emits nothing (up is the initial state); with a nil Live it emits down.
//   - Hard cap: when holding an event would exceed MaxBuffered, the held
//     event with the smallest (Time, Replica, ID) is emitted at once with
//     Late=true. Nothing is dropped and Buffered() never exceeds MaxBuffered.
//   - Output: drop-on-slow like Activity. When the output channel is full the
//     item is discarded and counted in Dropped; the merger never waits for a reader.
//   - Cancel: cancelling ctx stops every goroutine and closes out exactly once;
//     Done closes after that. Add after that returns false. The output also
//     stays open when all inputs are down, so that Add can bring them back.
//   - The merger never logs event or query text.

type mergeFakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*mergeFakeTimer
}

type mergeFakeTimer struct {
	at      time.Time
	ch      chan time.Time
	stopped bool
}

func (c *mergeFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mergeFakeClock) NewTimer(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &mergeFakeTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		t.ch <- c.now
		return t.ch, func() {}
	}
	c.timers = append(c.timers, t)
	return t.ch, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		t.stopped = true
	}
}

func (c *mergeFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, t := range c.timers {
		if !t.stopped && !t.at.After(c.now) {
			t.stopped = true
			t.ch <- c.now
		}
	}
}

var mergeT0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func mergeEv(replica string, id uint64, at time.Time) Event {
	return Event{ID: id, Replica: replica, Time: at, Kind: KindSearch, Query: "q"}
}

type mergeRig struct {
	t      *testing.T
	clock  *mergeFakeClock
	m      *Merger
	out    <-chan MergedItem
	hist   []Event
	cancel context.CancelFunc
}

func startMerger(t *testing.T, cfg MergeConfig, inputs ...MergeInput) *mergeRig {
	t.Helper()
	clock := &mergeFakeClock{now: mergeT0}
	cfg.Clock = clock
	m := NewMerger(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	hist, out := m.Start(ctx, inputs...)
	t.Cleanup(func() {
		cancel()
		select {
		case <-m.Done():
		case <-time.After(2 * time.Second):
			t.Error("merger did not stop after cancel")
		}
	})
	return &mergeRig{t: t, clock: clock, m: m, out: out, hist: hist, cancel: cancel}
}

func (r *mergeRig) recv() MergedItem {
	r.t.Helper()
	select {
	case it, ok := <-r.out:
		if !ok {
			r.t.Fatal("output closed, wanted an item")
		}
		return it
	case <-time.After(2 * time.Second):
		r.t.Fatal("no item on the output")
	}
	return MergedItem{}
}

func (r *mergeRig) recvEvent() Event {
	r.t.Helper()
	it := r.recv()
	if it.Event == nil || it.Status != nil {
		r.t.Fatalf("want an event item, got %+v", it)
	}
	return *it.Event
}

func (r *mergeRig) recvStatus() ReplicaStatus {
	r.t.Helper()
	it := r.recv()
	if it.Status == nil || it.Event != nil {
		r.t.Fatalf("want a status item, got %+v", it)
	}
	return *it.Status
}

func (r *mergeRig) expectNone() {
	r.t.Helper()
	select {
	case it, ok := <-r.out:
		r.t.Fatalf("unexpected output (open=%v): %+v", ok, it)
	case <-time.After(30 * time.Millisecond):
	}
}

func (r *mergeRig) waitFor(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func sendEv(t *testing.T, ch chan<- Event, e Event) {
	t.Helper()
	select {
	case ch <- e:
	case <-time.After(2 * time.Second):
		t.Fatal("merger did not read its input")
	}
}

func eventKeys(evs []Event) []string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Replica+"/"+string(rune('0'+e.ID)))
	}
	return s
}

func eqKeys(t *testing.T, got []Event, want ...string) {
	t.Helper()
	g := eventKeys(got)
	if strings.Join(g, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", g, want)
	}
}

// ---- history merge ----

func TestMergeHistories_OrderedByTimeThenReplicaThenID(t *testing.T) {
	a := []Event{mergeEv("b", 1, mergeT0), mergeEv("b", 2, mergeT0.Add(3*time.Second))}
	b := []Event{mergeEv("a", 2, mergeT0), mergeEv("a", 1, mergeT0), mergeEv("c", 1, mergeT0.Add(time.Second))}
	eqKeys(t, MergeHistories(a, b), "a/1", "a/2", "b/1", "c/1", "b/2")
	// Stable and deterministic: the argument order does not matter.
	eqKeys(t, MergeHistories(b, a), "a/1", "a/2", "b/1", "c/1", "b/2")
}

func TestMergeHistories_DedupsByReplicaAndIDFirstWins(t *testing.T) {
	first := mergeEv("a", 1, mergeT0)
	first.Query = "first"
	second := mergeEv("a", 1, mergeT0.Add(time.Hour))
	second.Query = "second"
	got := MergeHistories([]Event{first}, []Event{second, mergeEv("b", 1, mergeT0)})
	eqKeys(t, got, "a/1", "b/1")
	if got[0].Query != "first" {
		t.Fatalf("the first copy must win, got %q", got[0].Query)
	}
}

func TestMergeHistories_NoInputGivesNoEvents(t *testing.T) {
	if got := MergeHistories(); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if got := MergeHistories(nil, []Event{}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestMergeHistories_DoesNotMutateItsInputs(t *testing.T) {
	a := []Event{mergeEv("a", 2, mergeT0.Add(time.Second)), mergeEv("a", 1, mergeT0)}
	MergeHistories(a)
	eqKeys(t, a, "a/2", "a/1")
}

func TestMergerStart_ReturnsTheMergedHistoryOfAllInputs(t *testing.T) {
	r := startMerger(t, MergeConfig{},
		MergeInput{Name: "a", History: []Event{mergeEv("a", 1, mergeT0.Add(2*time.Second))}, Live: make(chan Event)},
		MergeInput{Name: "b", History: []Event{mergeEv("b", 1, mergeT0.Add(time.Second)), mergeEv("a", 1, mergeT0.Add(2*time.Second))}, Live: make(chan Event)},
	)
	eqKeys(t, r.hist, "b/1", "a/1")
	r.expectNone() // history is returned, not replayed on the output
}

// ---- reorder window ----

func TestMerger_DefaultWindowIsTwoSeconds(t *testing.T) {
	if DefaultReorderWindow != 2*time.Second {
		t.Fatalf("DefaultReorderWindow = %v", DefaultReorderWindow)
	}
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live})
	sendEv(t, live, mergeEv("a", 1, mergeT0))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(1999 * time.Millisecond)
	r.expectNone()
	r.clock.Advance(time.Millisecond)
	e := r.recvEvent()
	if e.Replica != "a" || e.ID != 1 || e.Late {
		t.Fatalf("event = %+v", e)
	}
	if r.m.Buffered() != 0 {
		t.Fatalf("Buffered = %d after release", r.m.Buffered())
	}
}

func TestMerger_WindowIsConfigurable(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{Window: 500 * time.Millisecond}, MergeInput{Name: "a", Live: live})
	sendEv(t, live, mergeEv("a", 1, mergeT0))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(499 * time.Millisecond)
	r.expectNone()
	r.clock.Advance(time.Millisecond)
	if e := r.recvEvent(); e.ID != 1 || e.Late {
		t.Fatalf("event = %+v", e)
	}
}

func TestMerger_EmitsLiveEventsFromAllInputsInTimestampOrder(t *testing.T) {
	la, lb := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: la}, MergeInput{Name: "b", Live: lb})
	r.clock.Advance(10 * time.Second)
	// a's event is newer but arrives first; b's older one arrives second.
	sendEv(t, la, mergeEv("a", 1, mergeT0.Add(10*time.Second)))
	r.waitFor("a buffered", func() bool { return r.m.Buffered() == 1 })
	sendEv(t, lb, mergeEv("b", 1, mergeT0.Add(9500*time.Millisecond)))
	r.waitFor("b buffered", func() bool { return r.m.Buffered() == 2 })

	r.clock.Advance(1500 * time.Millisecond) // b is due, a is not
	if e := r.recvEvent(); e.Replica != "b" || e.Late {
		t.Fatalf("first = %+v, want b on time", e)
	}
	r.expectNone()
	r.clock.Advance(500 * time.Millisecond)
	if e := r.recvEvent(); e.Replica != "a" || e.Late {
		t.Fatalf("second = %+v, want a on time", e)
	}
}

func TestMerger_DueEventsLeaveInTimeOrderEvenWhenReleasedTogether(t *testing.T) {
	la, lb := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: la}, MergeInput{Name: "b", Live: lb})
	sendEv(t, la, mergeEv("a", 1, mergeT0.Add(300*time.Millisecond)))
	r.waitFor("a", func() bool { return r.m.Buffered() == 1 })
	sendEv(t, lb, mergeEv("b", 1, mergeT0.Add(100*time.Millisecond)))
	r.waitFor("b", func() bool { return r.m.Buffered() == 2 })
	r.clock.Advance(time.Minute)
	if e := r.recvEvent(); e.Replica != "b" {
		t.Fatalf("first = %+v, want b", e)
	}
	if e := r.recvEvent(); e.Replica != "a" {
		t.Fatalf("second = %+v, want a", e)
	}
}

func TestMerger_EqualTimestampsLeaveByReplicaThenID(t *testing.T) {
	la, lb := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: la}, MergeInput{Name: "b", Live: lb})
	sendEv(t, lb, mergeEv("b", 1, mergeT0))
	sendEv(t, la, mergeEv("a", 2, mergeT0))
	sendEv(t, la, mergeEv("a", 1, mergeT0))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 3 })
	r.clock.Advance(2 * time.Second)
	var got []Event
	for range 3 {
		got = append(got, r.recvEvent())
	}
	eqKeys(t, got, "a/1", "a/2", "b/1")
}

// ---- dedup ----

func TestMerger_SameEventFromTwoInputsAppearsOnce(t *testing.T) {
	l1, l2 := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "direct", Live: l1}, MergeInput{Name: "via", Live: l2})
	e := mergeEv("a", 7, mergeT0)
	sendEv(t, l1, e)
	sendEv(t, l2, e)
	r.waitFor("duplicate counted", func() bool { return r.m.Duplicates() == 1 })
	r.clock.Advance(2 * time.Second)
	if got := r.recvEvent(); got.Replica != "a" || got.ID != 7 {
		t.Fatalf("event = %+v", got)
	}
	r.expectNone()
}

func TestMerger_HistoryEventRepeatedLiveIsNotEmitted(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", History: []Event{mergeEv("a", 1, mergeT0)}, Live: live})
	sendEv(t, live, mergeEv("a", 1, mergeT0))
	sendEv(t, live, mergeEv("a", 2, mergeT0.Add(time.Millisecond)))
	r.waitFor("duplicate and buffered", func() bool { return r.m.Duplicates() == 1 && r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.ID != 2 {
		t.Fatalf("event = %+v, want only a/2", e)
	}
	r.expectNone()
}

func TestMerger_CopyArrivingAfterTheFirstWasEmittedIsDropped(t *testing.T) {
	l1, l2 := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "p", Live: l1}, MergeInput{Name: "q", Live: l2})
	sendEv(t, l1, mergeEv("a", 1, mergeT0))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	r.recvEvent()
	sendEv(t, l2, mergeEv("a", 1, mergeT0))
	r.waitFor("duplicate", func() bool { return r.m.Duplicates() == 1 })
	r.clock.Advance(10 * time.Second)
	r.expectNone() // and not re-emitted as a late event
}

func TestMerger_DuplicateKeyKeepsTheFirstCopy(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live})
	first := mergeEv("a", 1, mergeT0)
	first.Query = "first"
	second := mergeEv("a", 1, mergeT0)
	second.Query = "second"
	sendEv(t, live, first)
	sendEv(t, live, second)
	r.waitFor("duplicate", func() bool { return r.m.Duplicates() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.Query != "first" {
		t.Fatalf("query = %q", e.Query)
	}
	r.expectNone()
}

func TestMerger_IDGoingBackwardsFromAPeerIsAcceptedWhenNew(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live})
	sendEv(t, live, mergeEv("a", 5, mergeT0))
	sendEv(t, live, mergeEv("a", 3, mergeT0.Add(time.Millisecond)))
	r.waitFor("both buffered", func() bool { return r.m.Buffered() == 2 })
	r.clock.Advance(2 * time.Second)
	got := []Event{r.recvEvent(), r.recvEvent()}
	eqKeys(t, got, "a/5", "a/3") // time order, not id order
	if r.m.Duplicates() != 0 || r.m.Rejected() != 0 {
		t.Fatalf("duplicates=%d rejected=%d", r.m.Duplicates(), r.m.Rejected())
	}
}

func TestMerger_RejectsEventsWithoutReplicaOrID(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live})
	sendEv(t, live, mergeEv("", 1, mergeT0))
	sendEv(t, live, mergeEv("a", 0, mergeT0))
	sendEv(t, live, mergeEv("a", 1, mergeT0))
	r.waitFor("two rejected, one buffered", func() bool { return r.m.Rejected() == 2 && r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.Replica != "a" || e.ID != 1 {
		t.Fatalf("event = %+v", e)
	}
	r.expectNone()
}

// ---- late events ----

func TestMerger_EventAfterTheWindowIsEmittedAtOnceMarkedLate(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live})
	r.clock.Advance(10 * time.Second)
	sendEv(t, live, mergeEv("a", 1, mergeT0.Add(7*time.Second))) // 3s old
	e := r.recvEvent()
	if !e.Late || e.ID != 1 || !e.Time.Equal(mergeT0.Add(7*time.Second)) {
		t.Fatalf("event = %+v, want late with its own timestamp", e)
	}
	if r.m.Buffered() != 0 {
		t.Fatalf("a late event must not be held, Buffered = %d", r.m.Buffered())
	}
}

func TestMerger_EventExactlyOneWindowOldIsNotLate(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live})
	r.clock.Advance(10 * time.Second)
	sendEv(t, live, mergeEv("a", 1, mergeT0.Add(8*time.Second)))
	if e := r.recvEvent(); e.Late {
		t.Fatalf("event = %+v, want on time", e)
	}
}

func TestMerger_LateEventsAreNeverDropped(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{OutputBuffer: 100}, MergeInput{Name: "a", Live: live})
	r.clock.Advance(time.Hour)
	for i := uint64(1); i <= 20; i++ {
		sendEv(t, live, mergeEv("a", i, mergeT0.Add(time.Duration(i)*time.Second)))
	}
	for i := uint64(1); i <= 20; i++ {
		if e := r.recvEvent(); !e.Late || e.ID != i {
			t.Fatalf("event %d = %+v", i, e)
		}
	}
	if r.m.Dropped() != 0 {
		t.Fatalf("Dropped = %d", r.m.Dropped())
	}
}

func TestMerger_ZeroAndAncientTimestampsAreLateNotDropped(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live})
	sendEv(t, live, mergeEv("a", 1, time.Time{}))
	sendEv(t, live, mergeEv("a", 2, mergeT0.Add(-24*365*time.Hour)))
	for i := uint64(1); i <= 2; i++ {
		if e := r.recvEvent(); !e.Late || e.ID != i {
			t.Fatalf("event = %+v, want late id %d", e, i)
		}
	}
}

func TestMerger_FarFutureTimestampWaitsAtMostOneWindow(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live})
	future := mergeT0.Add(1000 * time.Hour)
	sendEv(t, live, mergeEv("a", 1, future))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(1999 * time.Millisecond)
	r.expectNone()
	r.clock.Advance(time.Millisecond)
	e := r.recvEvent()
	if e.Late || !e.Time.Equal(future) {
		t.Fatalf("event = %+v, want on time with its original timestamp", e)
	}
}

func TestMerger_SkewedPeerDoesNotHoldBackOthers(t *testing.T) {
	la, lb := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "skewed", Live: la}, MergeInput{Name: "ok", Live: lb})
	sendEv(t, la, mergeEv("skewed", 1, mergeT0.Add(1000*time.Hour)))
	sendEv(t, lb, mergeEv("ok", 1, mergeT0))
	r.waitFor("both buffered", func() bool { return r.m.Buffered() == 2 })
	r.clock.Advance(2 * time.Second)
	got := []Event{r.recvEvent(), r.recvEvent()}
	eqKeys(t, got, "ok/1", "skewed/1")
}

// ---- replica_status ----

func TestMerger_ClosedInputEmitsDownWithLastSeen(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "peer-1", Live: live})
	r.clock.Advance(3 * time.Second)
	sendEv(t, live, mergeEv("peer-1", 1, mergeT0.Add(3*time.Second)))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(time.Millisecond) // before the window ends: only the status can come
	close(live)
	st := r.recvStatus()
	if st.Replica != "peer-1" || st.Up || !st.LastSeen.Equal(mergeT0.Add(3*time.Second)) {
		t.Fatalf("status = %+v", st)
	}
}

func TestMerger_ClosedInputWithoutEventsReportsAttachTimeAsLastSeen(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "peer-1", Live: live})
	r.clock.Advance(time.Minute)
	close(live)
	st := r.recvStatus()
	if st.Up || !st.LastSeen.Equal(mergeT0) {
		t.Fatalf("status = %+v, want down, last seen at attach", st)
	}
}

func TestMerger_NilLiveIsDownFromTheStart(t *testing.T) {
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "unreachable"})
	st := r.recvStatus()
	if st.Replica != "unreachable" || st.Up || !st.LastSeen.Equal(mergeT0) {
		t.Fatalf("status = %+v", st)
	}
}

func TestMerger_SilenceAloneNeverMarksAnInputDown(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "quiet", Live: live})
	r.clock.Advance(24 * time.Hour)
	r.expectNone()
}

func TestMerger_NoStatusWhenAnInputStartsUp(t *testing.T) {
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: make(chan Event)})
	r.expectNone()
}

func TestMerger_AddOfADownInputBringsItUp(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "p", Live: live})
	close(live)
	if st := r.recvStatus(); st.Up {
		t.Fatalf("status = %+v", st)
	}
	r.clock.Advance(5 * time.Second)
	again := make(chan Event)
	if !r.m.Add(MergeInput{Name: "p", Live: again}) {
		t.Fatal("Add of a down input must succeed")
	}
	st := r.recvStatus()
	if st.Replica != "p" || !st.Up || !st.LastSeen.Equal(mergeT0.Add(5*time.Second)) {
		t.Fatalf("status = %+v", st)
	}
	sendEv(t, again, mergeEv("p", 1, mergeT0.Add(5*time.Second)))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.Replica != "p" {
		t.Fatalf("event = %+v", e)
	}
}

func TestMerger_AddRefusesAnInputThatIsUpAndAnEmptyName(t *testing.T) {
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "p", Live: make(chan Event)})
	if r.m.Add(MergeInput{Name: "p", Live: make(chan Event)}) {
		t.Fatal("Add of an input that is up must be refused")
	}
	if r.m.Add(MergeInput{Name: "", Live: make(chan Event)}) {
		t.Fatal("Add with an empty name must be refused")
	}
	r.expectNone()
}

func TestMerger_AddOfANewInputAttachesItWithoutAStatus(t *testing.T) {
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: make(chan Event)})
	live := make(chan Event)
	if !r.m.Add(MergeInput{Name: "late-joiner", Live: live}) {
		t.Fatal("Add of a new input must succeed")
	}
	r.expectNone()
	sendEv(t, live, mergeEv("late-joiner", 1, mergeT0))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.Replica != "late-joiner" {
		t.Fatalf("event = %+v", e)
	}
}

func TestMerger_InputClosingMidWindowKeepsItsBufferedEvents(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "p", Live: live})
	sendEv(t, live, mergeEv("p", 1, mergeT0))
	sendEv(t, live, mergeEv("p", 2, mergeT0.Add(time.Millisecond)))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 2 })
	close(live)
	if st := r.recvStatus(); st.Up {
		t.Fatalf("status = %+v", st)
	}
	r.expectNone() // down is reported at once, the events still wait for the window
	r.clock.Advance(2 * time.Second)
	got := []Event{r.recvEvent(), r.recvEvent()}
	eqKeys(t, got, "p/1", "p/2")
	if got[0].Late || got[1].Late {
		t.Fatalf("events = %+v, want on time", got)
	}
}

// ---- no blocking, drop on slow ----

func TestMerger_StalledInputDoesNotBlockTheOthers(t *testing.T) {
	stalled, live := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "stalled", Live: stalled}, MergeInput{Name: "ok", Live: live})
	for i := uint64(1); i <= 3; i++ {
		sendEv(t, live, mergeEv("ok", i, mergeT0.Add(time.Duration(i)*time.Millisecond)))
	}
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 3 })
	r.clock.Advance(2 * time.Second)
	for i := uint64(1); i <= 3; i++ {
		if e := r.recvEvent(); e.Replica != "ok" || e.ID != i || e.Late {
			t.Fatalf("event = %+v", e)
		}
	}
}

func TestMerger_FullOutputDropsAndCountsInsteadOfWaiting(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{OutputBuffer: 2}, MergeInput{Name: "a", Live: live})
	r.clock.Advance(time.Hour)
	for i := uint64(1); i <= 5; i++ { // all late, so all go straight to the output
		sendEv(t, live, mergeEv("a", i, mergeT0.Add(time.Duration(i)*time.Second)))
	}
	r.waitFor("three dropped", func() bool { return r.m.Dropped() == 3 })
	if e := r.recvEvent(); e.ID != 1 {
		t.Fatalf("first = %+v", e)
	}
	if e := r.recvEvent(); e.ID != 2 {
		t.Fatalf("second = %+v", e)
	}
	r.expectNone()
	// The merger still works once the reader catches up.
	sendEv(t, live, mergeEv("a", 6, mergeT0.Add(6*time.Second)))
	if e := r.recvEvent(); e.ID != 6 {
		t.Fatalf("event = %+v", e)
	}
}

func TestMerger_StatusItemsAreDroppedOnSlowToo(t *testing.T) {
	a, b := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{OutputBuffer: 1}, MergeInput{Name: "a", Live: a}, MergeInput{Name: "b", Live: b})
	close(a)
	close(b)
	r.waitFor("one dropped", func() bool { return r.m.Dropped() == 1 })
	r.recvStatus()
}

// ---- hard cap ----

func TestMerger_AtTheCapTheOldestHeldEventLeavesEarlyMarkedLate(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{Window: time.Hour, MaxBuffered: 4, OutputBuffer: 100}, MergeInput{Name: "a", Live: live})
	for i := uint64(1); i <= 10; i++ {
		sendEv(t, live, mergeEv("a", i, mergeT0.Add(time.Duration(i)*time.Millisecond)))
	}
	for i := uint64(1); i <= 6; i++ {
		if e := r.recvEvent(); e.ID != i || !e.Late {
			t.Fatalf("event = %+v, want a/%d late", e, i)
		}
	}
	if r.m.Buffered() != 4 {
		t.Fatalf("Buffered = %d, want the cap 4", r.m.Buffered())
	}
	r.expectNone()
	r.clock.Advance(time.Hour)
	for i := uint64(7); i <= 10; i++ {
		if e := r.recvEvent(); e.ID != i || e.Late {
			t.Fatalf("event = %+v, want a/%d on time", e, i)
		}
	}
	if r.m.Dropped() != 0 {
		t.Fatalf("Dropped = %d", r.m.Dropped())
	}
}

func TestMerger_FloodFromOneInputStaysBoundedAndLosesNothing(t *testing.T) {
	const n = 1000
	flood, other := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{Window: time.Hour, MaxBuffered: 8, OutputBuffer: 2 * n},
		MergeInput{Name: "flood", Live: flood}, MergeInput{Name: "other", Live: other})
	for i := uint64(1); i <= n; i++ {
		sendEv(t, flood, mergeEv("flood", i, mergeT0.Add(time.Duration(i)*time.Microsecond)))
		if b := r.m.Buffered(); b > 8 {
			t.Fatalf("Buffered = %d, over the cap 8", b)
		}
	}
	sendEv(t, other, mergeEv("other", 1, mergeT0))
	seen := map[string]bool{}
	for len(seen) < n-8+1 { // everything over the cap has left, other's event included
		e := r.recvEvent()
		seen[e.Replica+"/"+string(rune(e.ID))] = true
	}
	r.clock.Advance(time.Hour)
	for len(seen) < n+1 {
		e := r.recvEvent()
		seen[e.Replica+"/"+string(rune(e.ID))] = true
	}
	if len(seen) != n+1 || r.m.Dropped() != 0 {
		t.Fatalf("seen %d distinct, dropped %d", len(seen), r.m.Dropped())
	}
}

// ---- cancellation ----

func TestMerger_CancelStopsEverythingAndClosesTheOutputOnce(t *testing.T) {
	before := runtime.NumGoroutine()
	closed := make(chan Event)
	close(closed)
	idle, busy := make(chan Event), make(chan Event)
	clock := &mergeFakeClock{now: mergeT0}
	m := NewMerger(MergeConfig{Clock: clock})
	ctx, cancel := context.WithCancel(context.Background())
	_, out := m.Start(ctx,
		MergeInput{Name: "closed", Live: closed},
		MergeInput{Name: "idle", Live: idle},
		MergeInput{Name: "busy", Live: busy},
	)
	sendEv(t, busy, mergeEv("busy", 1, mergeT0))
	cancel()
	cancel() // idempotent
	select {
	case <-m.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed after cancel")
	}
	for range out { // must end: the output is closed
	}
	if _, ok := <-out; ok {
		t.Fatal("output must stay closed")
	}
	if m.Add(MergeInput{Name: "x", Live: make(chan Event)}) {
		t.Fatal("Add after cancel must be refused")
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines: %d before, %d after cancel", before, after)
	}
}

func TestMerger_CancelWithAFullOutputAndNobodyReadingDoesNotHang(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{OutputBuffer: 1}, MergeInput{Name: "a", Live: live})
	r.clock.Advance(time.Hour)
	for i := uint64(1); i <= 5; i++ {
		sendEv(t, live, mergeEv("a", i, mergeT0))
	}
	r.cancel()
	select {
	case <-r.m.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed")
	}
}

// ---- logging ----

func TestMerger_NeverLogsQueryText(t *testing.T) {
	var buf bytes.Buffer
	oldSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	oldOut, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	defer func() {
		slog.SetDefault(oldSlog)
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	}()

	const secret = "SECRET-QUERY-TEXT"
	live := make(chan Event)
	r := startMerger(t, MergeConfig{Window: time.Hour, MaxBuffered: 1, OutputBuffer: 100},
		MergeInput{Name: "a", Live: live, History: []Event{{ID: 1, Replica: "a", Time: mergeT0, Kind: KindSearch, Query: secret}}})
	bad := mergeEv("", 0, mergeT0)
	bad.Query = secret
	sendEv(t, live, bad) // rejected
	dup := mergeEv("a", 1, mergeT0)
	dup.Query = secret
	sendEv(t, live, dup)              // duplicate of history
	for i := uint64(2); i <= 4; i++ { // over the cap
		e := mergeEv("a", i, mergeT0)
		e.Query = secret
		sendEv(t, live, e)
	}
	r.waitFor("processed", func() bool { return r.m.Rejected() == 1 && r.m.Duplicates() == 1 })
	close(live)
	for {
		if it := r.recv(); it.Status != nil {
			break
		}
	}
	r.cancel()
	<-r.m.Done()
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("query text was logged: %s", buf.String())
	}
}
