package platform

import (
	"strings"
	"testing"
	"time"
)

// withFloor sets the link-time seam for the test, as -X would.
func withFloor(t *testing.T, v string) {
	t.Helper()
	old := pollIntervalFloor
	pollIntervalFloor = v
	t.Cleanup(func() { pollIntervalFloor = old })
}

// #96: pollIntervalFloor, set only at link time, can lower the poll interval
// floor and nothing else: empty, unparseable, zero, negative or not shorter
// than MinPollInterval leaves the documented floor.
func TestPollIntervalFloorOnlyLowersTheFloor(t *testing.T) {
	for v, want := range map[string]time.Duration{
		"":      MinPollInterval,
		"100ms": 100 * time.Millisecond,
		"9s":    9 * time.Second,
		"10s":   MinPollInterval,
		"1h":    MinPollInterval,
		"0":     MinPollInterval,
		"-1s":   MinPollInterval,
		"soon":  MinPollInterval,
	} {
		withFloor(t, v)
		if got := PollIntervalFloor(); got != want {
			t.Errorf("pollIntervalFloor %q: PollIntervalFloor() = %v, want %v", v, got, want)
		}
	}
}

// LoadConfig applies the same floor; the environment cannot lower it: no
// variable, in any spelling, reaches the seam.
func TestLoadConfigAppliesTheLinkedFloorAndNoVariableLowersIt(t *testing.T) {
	env := func(extra map[string]string) Lookup {
		return func(k string) (string, bool) {
			v, ok := extra[k]
			return v, ok
		}
	}
	short := map[string]string{
		EnvPollInterval:              "200ms",
		"GOLDIE_POLL_INTERVAL_FLOOR": "1ms",
		"pollIntervalFloor":          "1ms",
		"POLLINTERVALFLOOR":          "1ms",
	}
	withFloor(t, "")
	_, err := LoadConfig(env(short))
	if err == nil || !strings.Contains(err.Error(), EnvPollInterval) || !strings.Contains(err.Error(), "10s") {
		t.Errorf("release floor: LoadConfig(200ms) = %v, want it refused naming %s and 10s", err, EnvPollInterval)
	}
	withFloor(t, "100ms")
	cfg, err := LoadConfig(env(short))
	if err != nil || cfg.PollInterval != 200*time.Millisecond {
		t.Errorf("linked floor 100ms: LoadConfig(200ms) = %v, %v; want 200ms accepted", cfg.PollInterval, err)
	}
}
