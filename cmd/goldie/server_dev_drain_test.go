package main

import (
	"os"
	"testing"
)

// devReportedDrain is GOLDIE_DRAIN_SECONDS in the startup report of p.
func devReportedDrain(t *testing.T, p *devProc) any {
	t.Helper()
	for _, m := range p.logRecords() {
		if m["msg"] == "config" {
			return m["GOLDIE_DRAIN_SECONDS"]
		}
	}
	t.Fatalf("no startup report\n%s", p.dump())
	return nil
}

// Nothing balances load in front of --dev, so without GOLDIE_DRAIN_SECONDS it
// stops at once instead of after the server's 25-second default; a set value
// still applies.
func TestServerDevDrainsForZeroSecondsUnlessSet(t *testing.T) {
	p := startDev(t, []string{"PATH=" + os.Getenv("PATH")}, contentDir(t))
	if got := devReportedDrain(t, p); got != float64(0) {
		t.Errorf("GOLDIE_DRAIN_SECONDS reported %v without the variable, want 0", got)
	}
	p.stop(t) // fails after 10s, well before a 25s drain ends

	p = startDev(t, devEnv("GOLDIE_DRAIN_SECONDS=1"), contentDir(t))
	if got := devReportedDrain(t, p); got != float64(1) {
		t.Errorf("GOLDIE_DRAIN_SECONDS reported %v with the variable at 1, want 1", got)
	}
	p.stop(t)
}
