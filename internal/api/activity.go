package api

import (
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/thschue/goldie/internal/contract"
)

// EventKind says what a recorded Event was: a search or a lookup.
type EventKind string

// The event kinds, as they appear on the wire.
const (
	KindSearch EventKind = "search"
	KindLookup EventKind = "lookup"
	KindGet    EventKind = "get"
)

const (
	// MaxActivitySubscribers caps concurrent subscribers to one Activity.
	MaxActivitySubscribers = 32
	// ActivitySubscriberBuffer is the capacity of every subscriber's live channel.
	ActivitySubscriberBuffer = 64
	// MaxActivityCapacity is the most events one Activity keeps; NewActivity
	// clamps a larger capacity to it. Every bounded string field of an Event
	// (Kind, Query, Token, ClientVersion, Project, TraceID, Served) is cut to
	// maxQueryBytes (1,024), so a full ring of events with 1,024-byte queries
	// is a worst case of about 12 to 15 MiB (10,000 events of 1.2 to 1.5 KiB).
	// That figure assumes the other fields stay short, as a token name, a
	// semver, a checked project id and a trace id do for a locally recorded
	// event; six full 1,024-byte fields would be about 60 MiB. 10,000 short
	// events are about 2 MiB. It is the contract's MaxActivityHistory, the
	// most events a peer reads before a stream's live marker.
	MaxActivityCapacity = contract.MaxActivityHistory
)

// Event is one search or lookup, as the activity feed shows it. Record
// assigns ID, Replica and Time; the caller sets the rest. Late is set only by
// the Merger, for an event that reached it after its reorder window.
type Event struct {
	ID      uint64
	Replica string
	Time    time.Time
	Kind    EventKind
	// Query is a search's words, or the article id a lookup asked for.
	Query string
	// Token is the name of the token that authenticated the request, never
	// its secret.
	Token string
	// ClientVersion is the X-Goldie-Client version in Semver.String form
	// ("1.5.0" for goldie/v1.5.0), or "" when the header is not exactly one
	// goldie/<semver> value.
	ClientVersion string
	// Project is the registered spelling of the project parameter, an
	// unregistered one lower-cased, or "" without one.
	Project string
	// TraceID is the trace id of the request's span, or "" without one.
	TraceID string
	// Served is the Reference of the Item served: the top result of a search,
	// or the Item of a lookup or get. "" on a miss.
	Served string
	// LatencyUS is the handler time in microseconds, never negative.
	LatencyUS int64
	// Miss is a search with zero results, or a lookup answering 404.
	Miss bool
	// Late is set only by the Merger: the event reached it after its reorder
	// window.
	Late bool
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

// NewActivity keeps the last capacity events (at least one, at most
// MaxActivityCapacity), stamping each with replica and the time from now. A
// nil now means time.Now.
func NewActivity(capacity int, replica string, now func() time.Time) *Activity {
	if now == nil {
		now = time.Now
	}
	return &Activity{
		replica: replica, now: now,
		ring: make([]Event, min(max(capacity, 1), MaxActivityCapacity)),
		next: 1, subs: map[*activitySub]struct{}{},
	}
}

// Record stores e with the next ID, the replica name and the current time,
// never a caller's Late,
// keeps at most maxQueryBytes of every string field (boundEvent), and offers
// it to every subscriber without waiting.
func (a *Activity) Record(e Event) {
	if a == nil || len(a.ring) == 0 {
		return
	}
	e.Replica = a.replica
	e.Late = false // only the Merger sets it
	e = boundEvent(e)
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
// nothing. TrySubscribe says so explicitly.
func (a *Activity) Subscribe() (history []Event, live <-chan Event, cancel func()) {
	history, live, cancel, _ = a.TrySubscribe()
	return history, live, cancel
}

// TrySubscribe is Subscribe with an explicit answer: ok is false when the hub
// is disabled or at MaxActivitySubscribers, and then history and live are nil
// and cancel is a non-nil no-op. It shares the cap and the slots with
// Subscribe.
func (a *Activity) TrySubscribe() (history []Event, live <-chan Event, cancel func(), ok bool) {
	if a == nil || len(a.ring) == 0 {
		return nil, nil, func() {}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.subs) >= MaxActivitySubscribers {
		return nil, nil, func() {}, false
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
	}, true
}

// Replica is the name its events carry.
func (a *Activity) Replica() string {
	if a == nil {
		return ""
	}
	return a.replica
}

// Dropped counts events a full subscriber channel refused.
func (a *Activity) Dropped() uint64 {
	if a == nil {
		return 0
	}
	return a.dropped.Load()
}

// boundEvent is the one normalise step for the string fields of an Event: it
// cuts Kind, Query, Token, ClientVersion, Project, TraceID and Served to a
// whole-rune prefix of at most maxQueryBytes, and clamps LatencyUS to zero or
// more. Replica is left alone: the Merger requires it to equal its input's name.
func boundEvent(e Event) Event {
	e.Kind = EventKind(truncateQuery(string(e.Kind)))
	e.Query = truncateQuery(e.Query)
	e.Token = truncateQuery(e.Token)
	e.ClientVersion = truncateQuery(e.ClientVersion)
	e.Project = truncateQuery(e.Project)
	e.TraceID = truncateQuery(e.TraceID)
	e.Served = truncateQuery(e.Served)
	e.LatencyUS = max(e.LatencyUS, 0)
	return e
}

// truncateQuery cuts q to at most maxQueryBytes without splitting a rune:
// when the cut lands inside one it drops that rune's leading bytes too. Bytes
// that are not valid UTF-8 are cut as they are, after at most three bytes of
// back-off.
func truncateQuery(q string) string {
	if len(q) <= maxQueryBytes {
		return q
	}
	cut := maxQueryBytes
	for i := 0; i < utf8.UTFMax-1 && !utf8.RuneStart(q[cut]); i++ {
		cut--
	}
	return q[:cut]
}
