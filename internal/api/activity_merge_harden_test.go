package api

import (
	"strings"
	"testing"
	"time"
)

// Hardening tests for the merger (#244 review round 1). They reuse the rig of
// activity_merge_test.go.

func TestMerger_AddAfterALongOutageSkipsHistoryAlreadyTaken(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "p", Live: live,
		History: []Event{mergeEv("p", 1, mergeT0), mergeEv("p", 2, mergeT0)}})
	sendEv(t, live, mergeEv("p", 3, mergeT0))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	r.recvEvent()
	close(live)
	if st := r.recvStatus(); st.Up {
		t.Fatalf("status = %+v", st)
	}
	r.clock.Advance(time.Hour) // far past the dedup retention

	now := mergeT0.Add(time.Hour)
	again := make(chan Event)
	ok := r.m.Add(MergeInput{Name: "p", Live: again, History: []Event{
		mergeEv("p", 1, mergeT0), mergeEv("p", 2, mergeT0), mergeEv("p", 3, mergeT0), mergeEv("p", 4, now),
	}})
	if !ok {
		t.Fatal("Add refused")
	}
	if st := r.recvStatus(); !st.Up {
		t.Fatalf("status = %+v", st)
	}
	r.waitFor("only the new one buffered", func() bool { return r.m.Buffered() == 1 && r.m.Duplicates() == 3 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.ID != 4 || e.Late {
		t.Fatalf("event = %+v, want p/4 on time", e)
	}
	r.expectNone()
}

func TestMerger_AddOfARestartedPeerWithResetIDsIsAccepted(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "p", Live: live,
		History: []Event{mergeEv("p", 7, mergeT0)}})
	close(live)
	r.recvStatus()
	r.clock.Advance(time.Hour)
	now := mergeT0.Add(time.Hour)
	if !r.m.Add(MergeInput{Name: "p", Live: make(chan Event), History: []Event{mergeEv("p", 1, now)}}) {
		t.Fatal("Add refused")
	}
	r.recvStatus()
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	if r.m.Duplicates() != 0 {
		t.Fatalf("Duplicates = %d", r.m.Duplicates())
	}
}

func TestMergeHistories_CutsOversizedQueriesAndDropsInvalidEvents(t *testing.T) {
	big := mergeEv("a", 1, mergeT0)
	big.Query = strings.Repeat("x", 5*maxQueryBytes)
	got := MergeHistories([]Event{big, mergeEv("", 2, mergeT0), mergeEv("a", 0, mergeT0), mergeEv("a", 3, mergeT0)})
	if len(got) != 2 || len(got[0].Query) > maxQueryBytes {
		t.Fatalf("got %d events, first query %d bytes", len(got), len(got[0].Query))
	}
	if got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestMerger_StartAndAddNormaliseHistoryLikeLiveEvents(t *testing.T) {
	big := mergeEv("a", 1, mergeT0)
	big.Query = strings.Repeat("x", 5*maxQueryBytes)
	late := mergeEv("a", 2, mergeT0)
	late.Late = true
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: make(chan Event),
		History: []Event{big, mergeEv("", 5, mergeT0), mergeEv("a", 0, mergeT0), late}})
	if len(r.hist) != 2 || len(r.hist[0].Query) > maxQueryBytes || r.hist[1].Late {
		t.Fatalf("history = %+v", r.hist)
	}
	if r.m.Rejected() != 2 {
		t.Fatalf("Rejected = %d", r.m.Rejected())
	}

	big2 := mergeEv("b", 1, mergeT0)
	big2.Query = strings.Repeat("y", 5*maxQueryBytes)
	if !r.m.Add(MergeInput{Name: "b", Live: make(chan Event), History: []Event{big2, mergeEv("b", 0, mergeT0)}}) {
		t.Fatal("Add refused")
	}
	r.waitFor("rejected", func() bool { return r.m.Rejected() == 3 && r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); len(e.Query) > maxQueryBytes {
		t.Fatalf("query is %d bytes", len(e.Query))
	}
}

func TestMergerStart_DuplicateInputNameFirstWinsSecondIsRejected(t *testing.T) {
	first, second := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{},
		MergeInput{Name: "p", Live: first, History: []Event{mergeEv("p", 1, mergeT0)}},
		MergeInput{Name: "p", Live: second, History: []Event{mergeEv("p", 2, mergeT0)}})
	eqKeys(t, r.hist, "p/1")
	if r.m.Rejected() != 1 {
		t.Fatalf("Rejected = %d", r.m.Rejected())
	}
	select {
	case second <- mergeEv("p", 9, mergeT0):
		t.Fatal("the second input must never be read")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestActivityRecord_ClearsACallersLate(t *testing.T) {
	a := NewActivity(4, "me", nil)
	a.Record(Event{Kind: KindSearch, Query: "q", Late: true})
	hist, _, cancel := a.Subscribe()
	defer cancel()
	if len(hist) != 1 || hist[0].Late {
		t.Fatalf("history = %+v", hist)
	}
}
