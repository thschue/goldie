package api

import (
	"testing"
	"time"
)

// Acceptance tests for #318: a feed carries only its own events (ADR 0022),
// so the merger rejects an event whose Replica differs from its input's Name.
// They reuse the rig of activity_merge_test.go.

func TestMerger_LiveEventClaimingAnotherReplicaIsRejectedAndTheRealOneStillComesThrough(t *testing.T) {
	la, lb := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: la}, MergeInput{Name: "b", Live: lb})
	spoof := mergeEv("b", 1, mergeT0)
	spoof.Query = "spoofed"
	sendEv(t, la, spoof) // peer a sends under b's name
	r.waitFor("spoof rejected", func() bool { return r.m.Rejected() == 1 })
	if r.m.Buffered() != 0 {
		t.Fatalf("the spoofed event must not be held, Buffered = %d", r.m.Buffered())
	}

	genuine := mergeEv("b", 1, mergeT0)
	genuine.Query = "real"
	sendEv(t, lb, genuine)
	r.waitFor("real event buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.Replica != "b" || e.ID != 1 || e.Query != "real" {
		t.Fatalf("event = %+v, want b's own b/1", e)
	}
	r.expectNone()
	if r.m.Rejected() != 1 || r.m.Duplicates() != 0 {
		t.Fatalf("Rejected = %d, Duplicates = %d, want 1 and 0", r.m.Rejected(), r.m.Duplicates())
	}
}

func TestMerger_SpoofAfterTheRealEventWasEmittedIsRejectedNotDeduplicated(t *testing.T) {
	la, lb := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: la}, MergeInput{Name: "b", Live: lb})
	sendEv(t, lb, mergeEv("b", 1, mergeT0))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	r.recvEvent()
	sendEv(t, la, mergeEv("b", 1, mergeT0))
	r.waitFor("rejected", func() bool { return r.m.Rejected() == 1 })
	if r.m.Duplicates() != 0 {
		t.Fatalf("Duplicates = %d, a spoof is rejected, not deduplicated", r.m.Duplicates())
	}
	r.clock.Advance(10 * time.Second)
	r.expectNone()
}

func TestMerger_OnlyTheSpoofedEventsOfAFeedAreRejected(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live})
	sendEv(t, live, mergeEv("a", 1, mergeT0))
	sendEv(t, live, mergeEv("b", 2, mergeT0))
	sendEv(t, live, mergeEv("a", 3, mergeT0))
	r.waitFor("one rejected, two buffered", func() bool { return r.m.Rejected() == 1 && r.m.Buffered() == 2 })
	r.clock.Advance(2 * time.Second)
	got := []Event{r.recvEvent(), r.recvEvent()}
	eqKeys(t, got, "a/1", "a/3")
	r.expectNone()
}

func TestMerger_ReplicaNameMustMatchTheInputNameExactly(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "peer-1", Live: live})
	names := []string{"Peer-1", "peer-1 ", " peer-1", "peer-1\x00", "peer-10", "peer-"}
	for i, name := range names {
		sendEv(t, live, mergeEv(name, uint64(i+1), mergeT0))
	}
	r.waitFor("all rejected", func() bool { return r.m.Rejected() == uint64(len(names)) })
	if r.m.Buffered() != 0 {
		t.Fatalf("Buffered = %d", r.m.Buffered())
	}
	r.clock.Advance(time.Minute)
	r.expectNone()
}

func TestMerger_SpoofedLiveEventsDoNotMoveTheVictimsHighWaterMark(t *testing.T) {
	// A spoof of b with a huge ID and a far-future Time must not make b's own
	// later replay look "already taken".
	la, lb := make(chan Event), make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: la}, MergeInput{Name: "b", Live: lb})
	sendEv(t, la, mergeEv("b", 1000, mergeT0.Add(1000*time.Hour)))
	r.waitFor("rejected", func() bool { return r.m.Rejected() == 1 })
	close(lb)
	if st := r.recvStatus(); st.Up {
		t.Fatalf("status = %+v", st)
	}
	r.clock.Advance(time.Hour)
	if !r.m.Add(MergeInput{Name: "b", Live: make(chan Event), History: []Event{mergeEv("b", 1, r.clock.Now())}}) {
		t.Fatal("Add refused")
	}
	if st := r.recvStatus(); !st.Up {
		t.Fatalf("status = %+v", st)
	}
	r.waitFor("b's own event buffered", func() bool { return r.m.Buffered() == 1 })
	if r.m.Duplicates() != 0 {
		t.Fatalf("Duplicates = %d", r.m.Duplicates())
	}
}

func TestMergerStart_HistoryClaimingAnotherReplicaIsRejectedAndTheRealOneStillComesThrough(t *testing.T) {
	spoof := mergeEv("b", 1, mergeT0)
	spoof.Query = "spoofed"
	genuine := mergeEv("b", 1, mergeT0)
	genuine.Query = "real"
	r := startMerger(t, MergeConfig{},
		// a comes first, so first-wins would favour the spoof.
		MergeInput{Name: "a", History: []Event{mergeEv("a", 1, mergeT0), spoof}, Live: make(chan Event)},
		MergeInput{Name: "b", History: []Event{genuine}, Live: make(chan Event)})
	eqKeys(t, r.hist, "a/1", "b/1")
	if r.hist[1].Query != "real" {
		t.Fatalf("history carries %q, want b's own event", r.hist[1].Query)
	}
	if r.m.Rejected() != 1 || r.m.Duplicates() != 0 {
		t.Fatalf("Rejected = %d, Duplicates = %d, want the spoof counted once and no duplicates", r.m.Rejected(), r.m.Duplicates())
	}
}

func TestMergerStart_SpoofedHistoryOfAnUnknownReplicaIsNotInTheHistory(t *testing.T) {
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", History: []Event{mergeEv("ghost", 1, mergeT0)}, Live: make(chan Event)})
	if len(r.hist) != 0 || r.m.Rejected() != 1 {
		t.Fatalf("history = %+v, Rejected = %d", r.hist, r.m.Rejected())
	}
}

func TestMerger_AddRejectsHistoryClaimingAnotherReplica(t *testing.T) {
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: make(chan Event)})
	if !r.m.Add(MergeInput{Name: "p", Live: make(chan Event), History: []Event{mergeEv("a", 9, mergeT0), mergeEv("p", 1, mergeT0)}}) {
		t.Fatal("Add refused")
	}
	r.waitFor("one rejected, one buffered", func() bool { return r.m.Rejected() == 1 && r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.Replica != "p" || e.ID != 1 {
		t.Fatalf("event = %+v", e)
	}
	r.expectNone()
}

func TestMerger_EveryRejectedEventIsCountedOnce(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "a", Live: live,
		History: []Event{mergeEv("", 1, mergeT0), mergeEv("b", 0, mergeT0), mergeEv("b", 5, mergeT0)}})
	sendEv(t, live, mergeEv("b", 0, mergeT0))
	sendEv(t, live, mergeEv("b", 6, mergeT0))
	r.waitFor("five rejected", func() bool { return r.m.Rejected() == 5 })
	r.expectNone()
	if r.m.Rejected() != 5 {
		t.Fatalf("Rejected = %d, want each event counted once", r.m.Rejected())
	}
}

// ---- the limits of the high-water mark (#321), each pinned by a test ----

func TestMerger_LiveEventsTheOutputDroppedAreNotRecoveredOnReattach(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{OutputBuffer: 1}, MergeInput{Name: "p", Live: live})
	r.clock.Advance(time.Hour)
	sendEv(t, live, mergeEv("p", 1, mergeT0.Add(time.Second))) // late: straight to the output
	sendEv(t, live, mergeEv("p", 2, mergeT0.Add(2*time.Second)))
	r.waitFor("p/2 dropped on the full output", func() bool { return r.m.Dropped() == 1 })
	if e := r.recvEvent(); e.ID != 1 {
		t.Fatalf("event = %+v", e)
	}
	close(live)
	if st := r.recvStatus(); st.Up {
		t.Fatalf("status = %+v", st)
	}
	r.clock.Advance(time.Hour) // far past the dedup retention

	if !r.m.Add(MergeInput{Name: "p", Live: make(chan Event), History: []Event{
		mergeEv("p", 1, mergeT0.Add(time.Second)), mergeEv("p", 2, mergeT0.Add(2*time.Second)),
	}}) {
		t.Fatal("Add refused")
	}
	if st := r.recvStatus(); !st.Up {
		t.Fatalf("status = %+v", st)
	}
	// The dropped p/2 is not recovered: the mark already covers it.
	r.waitFor("both replays skipped", func() bool { return r.m.Duplicates() == 2 })
	if r.m.Buffered() != 0 {
		t.Fatalf("Buffered = %d", r.m.Buffered())
	}
	r.expectNone()
}

func TestMerger_RestartedPeerWithResetIDsOnALaggingClockLosesThatOneReplay(t *testing.T) {
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "p", Live: live,
		History: []Event{mergeEv("p", 7, mergeT0.Add(10*time.Minute))}})
	close(live)
	if st := r.recvStatus(); st.Up {
		t.Fatalf("status = %+v", st)
	}
	r.clock.Advance(11 * time.Minute)

	// The peer restarted: IDs begin again at 1, but its clock lags the mark.
	if !r.m.Add(MergeInput{Name: "p", Live: make(chan Event), History: []Event{
		mergeEv("p", 1, mergeT0.Add(5*time.Minute)),  // id and time at or below the mark: lost
		mergeEv("p", 2, mergeT0.Add(11*time.Minute)), // newer than the mark: passes
	}}) {
		t.Fatal("Add refused")
	}
	if st := r.recvStatus(); !st.Up {
		t.Fatalf("status = %+v", st)
	}
	r.waitFor("p/1 skipped, p/2 held", func() bool { return r.m.Duplicates() == 1 && r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.ID != 2 {
		t.Fatalf("event = %+v, want only p/2", e)
	}
	r.expectNone()
}
