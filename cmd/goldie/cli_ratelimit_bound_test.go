package main

import (
	"strings"
	"testing"
)

// Acceptance test for #182 (from #200): a Retry-After above 3600 seconds is
// an unknown wait, so the CLI tells the user to wait a little and never
// prints the server's number. 3600 itself is still given as a wait (see
// TestCLIRateLimitedWaitIsTakenFromRetryAfter).
func TestCLIRateLimitedWithAWaitAboveAnHourSaysWaitALittle(t *testing.T) {
	for _, secs := range []string{"3601", "86400"} {
		url := limitedServer(t, secs)
		for _, args := range [][]string{{"search", "agent"}, {"lookup", "x"}} {
			_, err := runCLI(t, append(args, "--addr", url, "--token", cliToken)...)
			if err == nil {
				t.Errorf("Retry-After %s, %s: expected an error", secs, args[0])
				continue
			}
			msg := err.Error()
			if !strings.Contains(msg, "429") {
				t.Errorf("Retry-After %s, %s: error %q lacks the 429 status", secs, args[0], msg)
			}
			if !strings.Contains(msg, "Wait a little") {
				t.Errorf("Retry-After %s, %s: error %q does not fall back to \"Wait a little\"", secs, args[0], msg)
			}
			if strings.Contains(msg, secs) {
				t.Errorf("Retry-After %s, %s: error %q prints the wait above an hour", secs, args[0], msg)
			}
		}
	}
}
