package api

import (
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// EventKind says what a recorded Event was: a search or a lookup.
type EventKind string

// The event kinds, as they appear on the wire.
const (
	KindSearch EventKind = "search"
	KindLookup EventKind = "lookup"
)

const (
	// MaxActivitySubscribers caps concurrent subscribers to one Activity.
	MaxActivitySubscribers = 32
	// ActivitySubscriberBuffer is the capacity of every subscriber's live channel.
	ActivitySubscriberBuffer = 64
)

// Event is one search or lookup, as the activity feed shows it. Record
// assigns ID, Replica and Time; the caller sets Kind and Query.
type Event struct {
	ID      uint64
	Replica string
	Time    time.Time
	Kind    EventKind
	Query   string
}

// Activity is a fixed-capacity ring of recent Events with a hub that fans new
// ones out to subscribers. Record never blocks: a subscriber that cannot keep
// up loses events, counted in Dropped. A nil or zero Activity is disabled:
// Record does nothing and Subscribe refuses. Its methods are safe for
// concurrent use. It never logs query text.
type Activity struct {
	replica string
	now     func() time.Time

	mu      sync.Mutex
	ring    []Event // len is the capacity; nil when disabled
	next    uint64  // id of the next event
	subs    map[*activitySub]struct{}
	dropped atomic.Uint64
}

type activitySub struct {
	ch   chan Event
	once sync.Once
}

// NewActivity keeps the last capacity events (at least one), stamping each
// with replica and the time from now. A nil now means time.Now.
func NewActivity(capacity int, replica string, now func() time.Time) *Activity {
	if now == nil {
		now = time.Now
	}
	return &Activity{
		replica: replica, now: now,
		ring: make([]Event, max(capacity, 1)),
		next: 1, subs: map[*activitySub]struct{}{},
	}
}

// Record stores e with the next ID, the replica name and the current time,
// keeps at most maxQueryBytes of its query, and offers it to every
// subscriber without waiting.
func (a *Activity) Record(e Event) {
	if a == nil || len(a.ring) == 0 {
		return
	}
	e.Replica = a.replica
	e.Query = truncateQuery(e.Query)
	a.mu.Lock()
	defer a.mu.Unlock()
	e.ID = a.next
	e.Time = a.now()
	a.next++
	a.ring[(e.ID-1)%uint64(len(a.ring))] = e
	for s := range a.subs {
		select {
		case s.ch <- e:
		default:
			a.dropped.Add(1)
		}
	}
}

// Subscribe returns the retained events in ID order, then a channel of the
// events recorded after them, with no gap and no duplicate. cancel closes
// the channel and frees the slot; it is idempotent. When the hub is disabled
// or at MaxActivitySubscribers, history and live are nil and cancel does
// nothing.
func (a *Activity) Subscribe() (history []Event, live <-chan Event, cancel func()) {
	if a == nil || len(a.ring) == 0 {
		return nil, nil, func() {}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.subs) >= MaxActivitySubscribers {
		return nil, nil, func() {}
	}
	n := min(a.next-1, uint64(len(a.ring)))
	history = make([]Event, 0, n)
	for id := a.next - n; id < a.next; id++ {
		history = append(history, a.ring[(id-1)%uint64(len(a.ring))])
	}
	s := &activitySub{ch: make(chan Event, ActivitySubscriberBuffer)}
	a.subs[s] = struct{}{}
	return history, s.ch, func() {
		s.once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			delete(a.subs, s)
			close(s.ch) // Record sends under the same lock, so never on a closed channel
		})
	}
}

// Dropped counts events a full subscriber channel refused.
func (a *Activity) Dropped() uint64 {
	if a == nil {
		return 0
	}
	return a.dropped.Load()
}

// truncateQuery cuts q to maxQueryBytes without ending in half a rune.
func truncateQuery(q string) string {
	if len(q) <= maxQueryBytes {
		return q
	}
	q = q[:maxQueryBytes]
	for i := 0; i < utf8.UTFMax && len(q) > 0; i++ {
		if r, size := utf8.DecodeLastRuneInString(q); r != utf8.RuneError || size != 1 {
			break
		}
		q = q[:len(q)-1]
	}
	return q
}
