package api

import (
	"bytes"
	"log"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The contract these tests fix (the issue's description was a sketch):
//
//	type EventKind string
//	const KindSearch, KindLookup EventKind = "search", "lookup"
//	type Event struct {
//		ID      uint64    // assigned by Record, from 1, +1 per event
//		Replica string    // assigned by Record, the name given to NewActivity
//		Time    time.Time // assigned by Record from the clock given to NewActivity
//		Kind    EventKind // set by the caller
//		Query   string    // set by the caller; Record keeps at most maxQueryBytes bytes
//	}
//	const MaxActivitySubscribers = 32    // concurrent subscribers
//	const ActivitySubscriberBuffer = 64  // cap(live) of every subscriber
//	func NewActivity(capacity int, replica string, now func() time.Time) *Activity
//	func (a *Activity) Record(e Event)
//	func (a *Activity) Subscribe() (history []Event, live <-chan Event, cancel func())
//	func (a *Activity) Dropped() uint64
//
// Subscribe at the subscriber cap returns (nil, nil, cancel) with a non-nil
// no-op cancel; a nil live channel means "refused". cancel closes live, is
// idempotent and frees the slot. A nil *Activity and the zero Activity are
// disabled: Record is a no-op, Subscribe is refused, Dropped is 0. A nil
// clock means time.Now. A capacity below 1 keeps one event.

type activityClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *activityClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func newTestActivity(capacity int) (*Activity, *activityClock) {
	c := &activityClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	return NewActivity(capacity, "replica-a", c.now), c
}

func recordN(a *Activity, n int) {
	for range n {
		a.Record(Event{Kind: KindSearch, Query: "q"})
	}
}

func TestActivityEventCarriesMonotonicIDReplicaAndClockTime(t *testing.T) {
	a, clk := newTestActivity(8)
	start := clk.t
	a.Record(Event{ID: 999, Replica: "forged", Kind: KindSearch, Query: "alpha"})
	a.Record(Event{Kind: KindLookup, Query: "beta"})
	history, _, cancel := a.Subscribe()
	defer cancel()
	if len(history) != 2 {
		t.Fatalf("history has %d events, want 2", len(history))
	}
	for i, e := range history {
		if e.ID != uint64(i+1) {
			t.Errorf("event %d has id %d, want %d (assigned by Record, caller's id ignored)", i, e.ID, i+1)
		}
		if e.Replica != "replica-a" {
			t.Errorf("event %d replica %q, want replica-a (caller's value ignored)", i, e.Replica)
		}
		if want := start.Add(time.Duration(i+1) * time.Second); !e.Time.Equal(want) {
			t.Errorf("event %d time %v, want %v from the fake clock", i, e.Time, want)
		}
	}
	if history[0].Kind != KindSearch || history[0].Query != "alpha" || history[1].Kind != KindLookup || history[1].Query != "beta" {
		t.Errorf("history %+v lost kind or query", history)
	}
}

func TestActivityKindsAreDistinctNonEmptyValues(t *testing.T) {
	if KindSearch == "" || KindLookup == "" || KindSearch == KindLookup {
		t.Errorf("KindSearch %q and KindLookup %q must be distinct and non-empty", KindSearch, KindLookup)
	}
	if KindSearch != "search" || KindLookup != "lookup" {
		t.Errorf("kinds are %q and %q, want the wire values search and lookup", KindSearch, KindLookup)
	}
}

func TestActivityOverwritesTheOldestEventsAtCapacity(t *testing.T) {
	const capacity = 5
	a, _ := newTestActivity(capacity)
	recordN(a, capacity*4+2)
	history, _, cancel := a.Subscribe()
	defer cancel()
	if len(history) != capacity {
		t.Fatalf("history has %d events, want the capacity %d", len(history), capacity)
	}
	last := uint64(capacity*4 + 2)
	for i, e := range history {
		if want := last - capacity + 1 + uint64(i); e.ID != want {
			t.Errorf("history[%d].ID = %d, want %d (newest %d, ascending)", i, e.ID, want, capacity)
		}
	}
}

func TestActivityBelowCapacityKeepsEverythingInIDOrder(t *testing.T) {
	a, _ := newTestActivity(10)
	recordN(a, 3)
	history, _, cancel := a.Subscribe()
	defer cancel()
	if len(history) != 3 || history[0].ID != 1 || history[2].ID != 3 {
		t.Errorf("history %+v, want ids 1..3", history)
	}
}

func TestActivityEmptyHistoryIsEmptyAndLiveStartsAtOne(t *testing.T) {
	a, _ := newTestActivity(4)
	history, live, cancel := a.Subscribe()
	defer cancel()
	if len(history) != 0 || live == nil {
		t.Fatalf("history %v, live nil %v: want empty history and a live channel", history, live == nil)
	}
	a.Record(Event{Kind: KindLookup, Query: "x"})
	select {
	case e := <-live:
		if e.ID != 1 {
			t.Errorf("first live id %d, want 1", e.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recorded event never reached the subscriber")
	}
}

func TestActivityHistoryIsACopy(t *testing.T) {
	a, _ := newTestActivity(4)
	recordN(a, 2)
	h1, _, c1 := a.Subscribe()
	defer c1()
	h1[0].Query = "tampered"
	h1[0].ID = 4242
	h2, _, c2 := a.Subscribe()
	defer c2()
	if h2[0].Query != "q" || h2[0].ID != 1 {
		t.Errorf("mutating a subscriber's history changed the buffer: %+v", h2[0])
	}
}

// The boundary between history and live has no gap and no duplicate, even
// when events are recorded while Subscribe runs. Run under -race.
func TestActivityHistoryThenLiveHasNoGapAndNoDuplicateUnderConcurrentRecord(t *testing.T) {
	const total = ActivitySubscriberBuffer - 4 // fits the live buffer: nothing may be dropped
	for iter := range 300 {
		a, _ := newTestActivity(1000)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range total {
				a.Record(Event{Kind: KindSearch, Query: "q"})
				if iter%3 == 0 {
					runtime.Gosched()
				}
			}
		}()
		close(start)
		if iter%2 == 0 {
			runtime.Gosched()
		}
		history, live, cancel := a.Subscribe()
		wg.Wait()

		ids := make([]uint64, 0, total)
		for _, e := range history {
			ids = append(ids, e.ID)
		}
	drain:
		for len(ids) < total {
			select {
			case e := <-live:
				ids = append(ids, e.ID)
			case <-time.After(5 * time.Second):
				break drain
			}
		}
		cancel()
		if len(ids) != total {
			t.Fatalf("iteration %d: got %d events, want %d (history %d): %v", iter, len(ids), total, len(history), ids)
		}
		for i, id := range ids {
			if id != uint64(i+1) {
				t.Fatalf("iteration %d: event %d has id %d, want %d (gap or duplicate at the history/live boundary): %v", iter, i, id, i+1, ids)
			}
		}
		if a.Dropped() != 0 {
			t.Fatalf("iteration %d: %d dropped with room in the buffer", iter, a.Dropped())
		}
	}
}

func TestActivityRecordNeverBlocksOnASubscriberThatNeverReads(t *testing.T) {
	a, _ := newTestActivity(16)
	_, live, cancel := a.Subscribe()
	defer cancel()
	const total = ActivitySubscriberBuffer + 500
	done := make(chan struct{})
	go func() {
		defer close(done)
		recordN(a, total)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked on a subscriber that never reads")
	}
	if got, want := a.Dropped(), uint64(total-ActivitySubscriberBuffer); got != want {
		t.Errorf("Dropped() = %d, want %d (events beyond the subscriber's buffer)", got, want)
	}
	if cap(live) != ActivitySubscriberBuffer {
		t.Errorf("cap(live) = %d, want ActivitySubscriberBuffer %d", cap(live), ActivitySubscriberBuffer)
	}
	// The slow subscriber still gets the oldest buffered events in order.
	for want := uint64(1); want <= ActivitySubscriberBuffer; want++ {
		if e := <-live; e.ID != want {
			t.Fatalf("buffered event id %d, want %d", e.ID, want)
		}
	}
}

func TestActivityDroppedCountsOnlyTheSlowSubscriber(t *testing.T) {
	a, _ := newTestActivity(16)
	_, fast, c1 := a.Subscribe()
	defer c1()
	_, _, c2 := a.Subscribe() // never reads
	defer c2()
	n := ActivitySubscriberBuffer + 10
	for range n {
		a.Record(Event{Kind: KindSearch, Query: "q"})
		<-fast
	}
	if got := a.Dropped(); got != 10 {
		t.Errorf("Dropped() = %d, want 10 (only the slow subscriber loses events)", got)
	}
}

func TestActivityDroppedIsZeroWithoutSubscribers(t *testing.T) {
	a, _ := newTestActivity(4)
	recordN(a, 100)
	if a.Dropped() != 0 {
		t.Errorf("Dropped() = %d with no subscribers, want 0", a.Dropped())
	}
}

func TestActivityCapsConcurrentSubscribers(t *testing.T) {
	a, _ := newTestActivity(4)
	recordN(a, 2)
	cancels := make([]func(), 0, MaxActivitySubscribers)
	for i := range MaxActivitySubscribers {
		_, live, cancel := a.Subscribe()
		if live == nil {
			t.Fatalf("subscriber %d refused below the cap of %d", i+1, MaxActivitySubscribers)
		}
		cancels = append(cancels, cancel)
	}
	history, live, cancel := a.Subscribe()
	if history != nil || live != nil || cancel == nil {
		t.Fatalf("over the cap: history %v, live nil %v, cancel nil %v; want nil, nil and a non-nil cancel", history, live == nil, cancel == nil)
	}
	cancel() // no-op, must not free a slot or panic
	cancel()
	if _, l, c := a.Subscribe(); l != nil {
		c()
		t.Fatal("cancelling a refused subscription freed a slot")
	}
	// A refused Subscribe must not stop Record.
	recordN(a, 3)

	cancels[7]()
	_, l, c := a.Subscribe()
	if l == nil {
		t.Fatal("cancel did not release the subscriber's slot")
	}
	c()
	for _, c := range cancels {
		c()
	}
}

func TestActivityCancelIsIdempotentAndClosesLive(t *testing.T) {
	a, _ := newTestActivity(4)
	_, live, cancel := a.Subscribe()
	cancel()
	cancel()
	cancel()
	select {
	case _, ok := <-live:
		if ok {
			t.Error("live delivered an event after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("live is not closed after cancel")
	}
	// A double cancel must not free a slot twice: fill the hub, cancel one
	// subscriber twice, and only one new subscriber may fit.
	cancels := make([]func(), 0, MaxActivitySubscribers)
	for range MaxActivitySubscribers {
		_, l, c := a.Subscribe()
		if l == nil {
			t.Fatal("double cancel leaked a slot")
		}
		cancels = append(cancels, c)
	}
	cancels[0]()
	cancels[0]()
	_, l1, c1 := a.Subscribe()
	_, l2, c2 := a.Subscribe()
	if l1 == nil || l2 != nil {
		t.Errorf("after one subscriber left (cancelled twice): next fits %v, the one after fits %v; want true, false", l1 != nil, l2 != nil)
	}
	c1()
	c2()
	for _, c := range cancels {
		c()
	}
}

func TestActivityRecordAfterEverySubscriberCancelledIsFine(t *testing.T) {
	a, _ := newTestActivity(4)
	var cancels []func()
	for range 5 {
		_, _, c := a.Subscribe()
		cancels = append(cancels, c)
	}
	for _, c := range cancels {
		c()
	}
	recordN(a, 20)
	if a.Dropped() != 0 {
		t.Errorf("Dropped() = %d after every subscriber left, want 0", a.Dropped())
	}
	history, _, cancel := a.Subscribe()
	defer cancel()
	if len(history) != 4 || history[3].ID != 20 {
		t.Errorf("history %+v, want the last 4 of 20 events", history)
	}
}

func TestActivityCancelRacingRecordAndSubscribeIsSafe(t *testing.T) {
	a, _ := newTestActivity(32)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				a.Record(Event{Kind: KindLookup, Query: "q"})
			}
		}
	}()
	var subs sync.WaitGroup
	for range 8 {
		subs.Add(1)
		go func() {
			defer subs.Done()
			for range 200 {
				_, live, cancel := a.Subscribe()
				if live != nil {
					select {
					case <-live:
					default:
					}
				}
				cancel()
				cancel()
			}
		}()
	}
	subs.Wait()
	close(stop)
	wg.Wait()
}

func TestActivityLeaksNoGoroutines(t *testing.T) {
	a, _ := newTestActivity(8)
	before := runtime.NumGoroutine()
	for range 200 {
		_, _, cancel := a.Subscribe()
		a.Record(Event{Kind: KindSearch, Query: "q"})
		cancel()
	}
	var after int
	for range 100 {
		runtime.GC()
		after = runtime.NumGoroutine()
		if after <= before {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines went from %d to %d after 200 subscribe/cancel cycles", before, after)
}

// Memory is bounded by capacity times event size: the hub keeps at most
// maxQueryBytes of the query, whatever the caller passes.
func TestActivityTruncatesALongQueryToTheSpecCap(t *testing.T) {
	a, _ := newTestActivity(4)
	long := strings.Repeat("a", maxQueryBytes) + strings.Repeat("b", 1<<20)
	a.Record(Event{Kind: KindSearch, Query: long})
	a.Record(Event{Kind: KindSearch, Query: strings.Repeat("c", maxQueryBytes)})
	history, _, cancel := a.Subscribe()
	defer cancel()
	if got := history[0].Query; len(got) != maxQueryBytes || got != long[:maxQueryBytes] {
		t.Errorf("stored a %d-byte query, want the first %d bytes", len(got), maxQueryBytes)
	}
	if len(history[1].Query) != maxQueryBytes {
		t.Errorf("a query at the cap was changed to %d bytes", len(history[1].Query))
	}
}

func TestActivityLiveEventsAreTruncatedToo(t *testing.T) {
	a, _ := newTestActivity(4)
	_, live, cancel := a.Subscribe()
	defer cancel()
	a.Record(Event{Kind: KindSearch, Query: strings.Repeat("z", 5000)})
	if e := <-live; len(e.Query) != maxQueryBytes {
		t.Errorf("live event carries %d query bytes, want %d", len(e.Query), maxQueryBytes)
	}
}

func TestActivityCapacityBelowOneKeepsOneEventAndDoesNotPanic(t *testing.T) {
	for _, capacity := range []int{0, -3} {
		a, _ := newTestActivity(capacity)
		recordN(a, 3)
		history, _, cancel := a.Subscribe()
		if len(history) != 1 || history[0].ID != 3 {
			t.Errorf("capacity %d: history %+v, want only event 3", capacity, history)
		}
		cancel()
	}
}

func TestActivityNilAndZeroValueAreSafeAndDisabled(t *testing.T) {
	var nilActivity *Activity
	for name, a := range map[string]*Activity{"nil": nilActivity, "zero": {}} {
		a.Record(Event{Kind: KindSearch, Query: "x"})
		history, live, cancel := a.Subscribe()
		if history != nil || live != nil || cancel == nil {
			t.Errorf("%s: history %v, live nil %v, cancel nil %v; want refused with a non-nil cancel", name, history, live == nil, cancel == nil)
			continue
		}
		cancel()
		cancel()
		if a.Dropped() != 0 {
			t.Errorf("%s: Dropped() = %d, want 0", name, a.Dropped())
		}
	}
}

func TestActivityNilClockStillRecords(t *testing.T) {
	a := NewActivity(4, "r", nil)
	a.Record(Event{Kind: KindSearch, Query: "x"})
	history, _, cancel := a.Subscribe()
	defer cancel()
	if len(history) != 1 || history[0].Time.IsZero() {
		t.Errorf("history %+v, want one event stamped with real time", history)
	}
}

// Stored query text is never logged: recording, subscribing, overflowing a
// slow subscriber and cancelling write to no logger.
func TestActivityNeverLogsQueryText(t *testing.T) {
	var slogBuf, stdBuf bytes.Buffer
	oldSlog, oldOut, oldFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(&stdBuf)
	defer func() {
		slog.SetDefault(oldSlog)
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	}()

	const secret = "s3cret-query-text"
	a, _ := newTestActivity(4)
	_, _, cancel := a.Subscribe()
	for range ActivitySubscriberBuffer + 20 { // forces drops
		a.Record(Event{Kind: KindSearch, Query: secret})
	}
	_, _, c2 := a.Subscribe()
	c2()
	cancel()
	_ = a.Dropped()
	if slogBuf.Len() != 0 || stdBuf.Len() != 0 {
		t.Errorf("the hub logged: slog %q, log %q", slogBuf.String(), stdBuf.String())
	}
}
