package api

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// A peer that stays down is a warning once, and debug after; coming up again
// makes the next failure a warning.
func TestADownPeerWarnsOnceUntilItComesUp(t *testing.T) {
	var buf bytes.Buffer
	f := &federation{log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), downs: &downSet{}}
	levels := func() string {
		var out []string
		for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			out = append(out, strings.Fields(l)[1])
		}
		buf.Reset()
		return strings.Join(out, " ")
	}
	f.down("goldie-1", "open_failed")
	f.down("goldie-1", "open_failed")
	f.down("goldie-2", "open_failed")
	if got := levels(); got != "level=WARN level=DEBUG level=WARN" {
		t.Errorf("levels %q, want WARN then DEBUG for one peer, WARN for another", got)
	}
	f.downs.up("goldie-1")
	f.down("goldie-1", "stream_ended")
	if got := levels(); got != "level=WARN" {
		t.Errorf("after coming up: %q, want WARN", got)
	}
	for i := range 2 * maxDownNames {
		f.down(strings.Repeat("n", i+1), "open_failed")
	}
	if n := len(f.downs.names); n > maxDownNames {
		t.Errorf("%d names remembered, want at most %d", n, maxDownNames)
	}
}
