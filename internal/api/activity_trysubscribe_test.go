package api

import (
	"testing"
	"time"
)

// Acceptance tests for #243, from #310: a refused subscription is explicit.
// Subscribe signals refusal with a nil live channel, which a caller that
// forgets to check blocks on forever. The contract these tests fix:
//
//	// TrySubscribe is Subscribe with an explicit answer: ok is false when the
//	// hub is disabled or at MaxActivitySubscribers, and then history and live
//	// are nil and cancel is a non-nil no-op. It shares the cap and the slots
//	// with Subscribe.
//	func (a *Activity) TrySubscribe() (history []Event, live <-chan Event, cancel func(), ok bool)
//
// Subscribe keeps its signature (the #241 tests call it).

func TestTrySubscribeBelowTheCapIsOKWithHistoryAndLive(t *testing.T) {
	a, _ := newTestActivity(8)
	recordN(a, 3)
	history, live, cancel, ok := a.TrySubscribe()
	if !ok {
		t.Fatal("TrySubscribe refused the first subscriber")
	}
	defer cancel()
	if live == nil || cancel == nil {
		t.Fatalf("ok subscription: live nil %v, cancel nil %v; want both set", live == nil, cancel == nil)
	}
	if len(history) != 3 || history[0].ID != 1 || history[2].ID != 3 {
		t.Errorf("history %+v, want ids 1..3", history)
	}
	a.Record(Event{Kind: KindSearch, Query: "next"})
	select {
	case e := <-live:
		if e.ID != 4 || e.Query != "next" {
			t.Errorf("first live event %+v, want id 4 \"next\"", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a recorded event never reached the subscriber")
	}
}

func TestTrySubscribeAtTheCapIsRefusedExplicitly(t *testing.T) {
	a, _ := newTestActivity(4)
	recordN(a, 2)
	cancels := make([]func(), 0, MaxActivitySubscribers)
	for i := range MaxActivitySubscribers {
		_, _, cancel, ok := a.TrySubscribe()
		if !ok {
			t.Fatalf("subscriber %d refused below the cap of %d", i+1, MaxActivitySubscribers)
		}
		cancels = append(cancels, cancel)
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	history, live, cancel, ok := a.TrySubscribe()
	if ok {
		t.Fatal("TrySubscribe answered ok over the subscriber cap")
	}
	if history != nil || live != nil || cancel == nil {
		t.Fatalf("refused: history %v, live nil %v, cancel nil %v; want nil, nil and a non-nil cancel", history, live == nil, cancel == nil)
	}
	cancel() // a no-op: it must neither panic nor free a slot
	cancel()
	if _, _, c, ok := a.TrySubscribe(); ok {
		c()
		t.Fatal("cancelling a refused subscription freed a slot")
	}
	// A refused subscriber does not stop Record.
	recordN(a, 3)

	cancels[0]()
	_, _, c, ok := a.TrySubscribe()
	if !ok {
		t.Fatal("cancel did not release the subscriber's slot")
	}
	c()
}

// TrySubscribe and Subscribe count against one cap: neither is a way around
// the other's limit.
func TestTrySubscribeSharesTheCapWithSubscribe(t *testing.T) {
	a, _ := newTestActivity(4)
	var cancels []func()
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	for i := range MaxActivitySubscribers {
		if i%2 == 0 {
			_, live, c := a.Subscribe()
			if live == nil {
				t.Fatalf("Subscribe %d refused below the cap", i+1)
			}
			cancels = append(cancels, c)
			continue
		}
		_, _, c, ok := a.TrySubscribe()
		if !ok {
			t.Fatalf("TrySubscribe %d refused below the cap", i+1)
		}
		cancels = append(cancels, c)
	}
	if _, _, c, ok := a.TrySubscribe(); ok {
		c()
		t.Error("TrySubscribe answered ok with the cap filled by both methods")
	}
	if _, live, c := a.Subscribe(); live != nil {
		c()
		t.Error("Subscribe was let in with the cap filled by both methods")
	}
}

func TestTrySubscribeOnADisabledHubIsRefused(t *testing.T) {
	var nilActivity *Activity
	for name, a := range map[string]*Activity{"nil": nilActivity, "zero": {}} {
		history, live, cancel, ok := a.TrySubscribe()
		if ok || history != nil || live != nil || cancel == nil {
			t.Errorf("%s: ok %v, history %v, live nil %v, cancel nil %v; want refused with a non-nil cancel",
				name, ok, history, live == nil, cancel == nil)
			continue
		}
		cancel()
		cancel()
	}
}

// cancel closes live and frees the slot, once, however often it is called.
func TestTrySubscribeCancelClosesLiveAndIsIdempotent(t *testing.T) {
	a, _ := newTestActivity(4)
	_, live, cancel, ok := a.TrySubscribe()
	if !ok {
		t.Fatal("refused")
	}
	cancel()
	cancel()
	select {
	case _, open := <-live:
		if open {
			t.Error("live delivered an event after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("live is not closed after cancel")
	}
	// Every slot is free again: the full cap fits.
	var cancels []func()
	for i := range MaxActivitySubscribers {
		_, _, c, ok := a.TrySubscribe()
		if !ok {
			t.Fatalf("subscriber %d refused after the only other one cancelled (twice)", i+1)
		}
		cancels = append(cancels, c)
	}
	for _, c := range cancels {
		c()
	}
}
