package engine

import (
	"errors"
	"testing"
	"time"
)

// #479: a SourceSet stamps a poll's status as a Syncer does, with its own
// clock, runs a poll loop per Source and has no Current before a load.

func TestSourceSetStampsPollsWithItsClockLikeASyncer(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	src := &clockedSource{clock: &clock, step: 3 * time.Second, snap: snapOf("a")}
	set := NewSourceSet(NamedSource{Name: "one", Source: src})
	set.now = func() time.Time { return clock }

	if set.Current() != nil {
		t.Error("Current before a load is not nil")
	}
	if err := set.Poll(t.Context(), "one"); err != nil {
		t.Fatal(err)
	}
	st := set.Status("one")
	if st.Duration != 3*time.Second || !st.LastPoll.Equal(clock) || !st.LastSuccess.Equal(clock) {
		t.Errorf("status after an ok poll = %+v, want Duration 3s and both stamps at the clock", st)
	}
	if set.Current() == nil {
		t.Error("Current after a load is nil")
	}

	src.step, src.snap, src.err = 2*time.Second, nil, errors.New("boom")
	if err := set.Poll(t.Context(), "one"); err == nil {
		t.Fatal("want error")
	}
	failed := set.Status("one")
	if failed.Duration != 2*time.Second || failed.LastError == "" || !failed.LastSuccess.Equal(st.LastSuccess) {
		t.Errorf("status after a failed poll = %+v, want Duration 2s, the error and the old LastSuccess", failed)
	}
}

func TestSourceSetRunPollsUntilTheContextEnds(t *testing.T) {
	src := &fakeSource{snap: snapOf("a"), rev: "r1"}
	set := NewSourceSet(NamedSource{Name: "one", Source: src})
	var handed []SyncStatus
	set.Run(t.Context(), "one", 0, func(st SyncStatus) { handed = append(handed, st) })
	if len(handed) != 1 || handed[0].Revision != "r1" {
		t.Errorf("onPoll got %+v, want one status at r1", handed)
	}

	set.Run(t.Context(), "nobody", 0, func(SyncStatus) { t.Error("a loop ran for a name that is no Source") })
}
