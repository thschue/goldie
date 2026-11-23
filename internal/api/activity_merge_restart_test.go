package api

import (
	"testing"
	"time"
)

// #398 (slice 2 of #245): a StatefulSet pod that restarts keeps its name and
// starts its ids at 1 again. Re-attached through Add within the dedup
// retention (a second after it went down here, well inside four windows), its
// new events must not be taken for its old ones: they are newer than the
// input's high-water mark, so they come through whether they arrive live or in
// the re-attached History. A copy of an event the old process already
// delivered (same id, same time) is still a duplicate. They reuse the rig of
// activity_merge_test.go.

// restartRig starts a Merger with one input p, takes p/1 at mergeT0 through
// it, and closes p's feed, so p is down with p/1 in the dedup set.
func restartRig(t *testing.T) *mergeRig {
	t.Helper()
	live := make(chan Event)
	r := startMerger(t, MergeConfig{}, MergeInput{Name: "p", Live: live})
	sendEv(t, live, mergeEv("p", 1, mergeT0))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 })
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.ID != 1 || !e.Time.Equal(mergeT0) {
		t.Fatalf("event = %+v, want the old process's p/1", e)
	}
	close(live)
	if st := r.recvStatus(); st.Up {
		t.Fatalf("status = %+v, want p down", st)
	}
	r.clock.Advance(time.Second)
	return r
}

func TestMerger_RestartedPeerReattachedSoonGetsItsNewLiveEventsThrough(t *testing.T) {
	r := restartRig(t)
	again := make(chan Event)
	if !r.m.Add(MergeInput{Name: "p", Live: again}) {
		t.Fatal("Add refused")
	}
	if st := r.recvStatus(); !st.Up {
		t.Fatalf("status = %+v, want p up", st)
	}
	restarted := mergeT0.Add(3 * time.Second)
	sendEv(t, again, mergeEv("p", 1, restarted))
	r.waitFor("buffered", func() bool { return r.m.Buffered() == 1 || r.m.Duplicates() > 0 })
	if r.m.Duplicates() != 0 {
		t.Fatalf("Duplicates = %d: the restarted peer's new p/1 was taken for its old one", r.m.Duplicates())
	}
	r.clock.Advance(2 * time.Second)
	if e := r.recvEvent(); e.ID != 1 || !e.Time.Equal(restarted) || e.Late {
		t.Fatalf("event = %+v, want the restarted peer's new p/1 on time", e)
	}

	// The new p/1 sent again, and the old process's p/1, are both duplicates.
	sendEv(t, again, mergeEv("p", 1, restarted))
	sendEv(t, again, mergeEv("p", 1, mergeT0))
	r.waitFor("both duplicates", func() bool { return r.m.Duplicates() == 2 })
	r.clock.Advance(10 * time.Second)
	r.expectNone()
}

func TestMerger_RestartedPeerReattachedSoonGetsItsNewHistoryThrough(t *testing.T) {
	r := restartRig(t)
	restarted := mergeT0.Add(3 * time.Second)
	if !r.m.Add(MergeInput{Name: "p", Live: make(chan Event), History: []Event{
		mergeEv("p", 1, mergeT0),   // the old process's p/1, replayed: skipped
		mergeEv("p", 1, restarted), // the restarted process's p/1: new
		mergeEv("p", 2, restarted.Add(time.Millisecond)),
	}}) {
		t.Fatal("Add refused")
	}
	if st := r.recvStatus(); !st.Up {
		t.Fatalf("status = %+v, want p up", st)
	}
	r.waitFor("the replay skipped", func() bool { return r.m.Duplicates() >= 1 })
	if r.m.Duplicates() != 1 || r.m.Buffered() != 2 {
		t.Fatalf("Duplicates = %d, Buffered = %d; want 1 (the old p/1) and 2 (the new p/1 and p/2)", r.m.Duplicates(), r.m.Buffered())
	}
	r.clock.Advance(2 * time.Second)
	got := []Event{r.recvEvent(), r.recvEvent()}
	eqKeys(t, got, "p/1", "p/2")
	if !got[0].Time.Equal(restarted) {
		t.Fatalf("event = %+v, want the restarted peer's new p/1", got[0])
	}
	r.expectNone()
}

// A peer that restarts again and again, each time sending fewer events than
// the dedup bound, must not grow the Merger: Add forgets its keys, so the
// queue of seen keys, not only the set, is what the bound has to cap.
func TestMerger_RepeatedRestartsKeepTheDedupQueueBounded(t *testing.T) {
	r := startMerger(t, MergeConfig{MaxBuffered: 1}) // a bound of 64 keys
	go func() {
		for range r.out { // drained: only the Merger's memory is checked
		}
	}()
	down := func() bool {
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		st, ok := r.m.inputs["p"]
		return ok && !st.up
	}
	var id uint64
	for round := range 40 {
		live := make(chan Event)
		if !r.m.Add(MergeInput{Name: "p", Live: live}) {
			t.Fatalf("round %d: Add refused", round)
		}
		for range 50 {
			id++
			sendEv(t, live, mergeEv("p", id, mergeT0.Add(time.Duration(id)*time.Millisecond)))
		}
		close(live)
		r.waitFor("p down", down)
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	if q := len(r.m.seenFIFO) - r.m.seenHead; q > r.m.seenMax || len(r.m.seen) > r.m.seenMax {
		t.Errorf("after 2000 events in 40 restarts: %d queued keys, %d seen keys; want both at most %d", q, len(r.m.seen), r.m.seenMax)
	}
}
