package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// #253: a hand-built RateLimit whose Per/Requests rounds to zero limits
// nothing, so PublicHandler refuses it like a zero Requests or Per. It cannot
// come from the environment (ParseRateLimit caps the rate at 1e9/s).

// roundsToZeroLine is the exact line PublicHandler reports. It names the
// variable and never a value.
const roundsToZeroLine = platform.EnvRateLimit + ": the interval per request rounds to zero; the server does not run without a limit"

func TestPublicHandlerRefusesARateLimitIntervalThatRoundsToZero(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limit  platform.RateLimit
		refuse bool
	}{
		{"zero: 1ns shared by two requests", platform.RateLimit{Requests: 2, Per: time.Nanosecond, Burst: 1}, true},
		{"zero: huge requests, tiny period", platform.RateLimit{Requests: 1 << 40, Per: time.Second, Burst: 1}, true},
		{"zero: one over 1e9 a second", platform.RateLimit{Requests: 1_000_000_001, Per: time.Second, Burst: 1}, true},
		{"one nanosecond is the smallest accepted interval", platform.RateLimit{Requests: 1_000_000_000, Per: time.Second, Burst: 1}, false},
		{"one nanosecond, one request", platform.RateLimit{Requests: 1, Per: time.Nanosecond, Burst: 1}, false},
		{"normal", platform.RateLimit{Requests: 40, Per: time.Second, Burst: 40}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := platform.Config{ContentDir: testContent, APITokens: namedTokenLines(), RateLimit: tc.limit}
			if !tc.refuse {
				h, err := PublicHandler(cfg, nil)
				if err != nil || h == nil {
					t.Fatalf("PublicHandler(%+v) = %v, %v, want a handler", tc.limit, h, err)
				}
				return
			}
			lines, err := configErrors(t, tc.name, cfg)
			if len(lines) != 1 || lines[0] != roundsToZeroLine {
				t.Errorf("err = %q, want exactly the line %q", err, roundsToZeroLine)
			}
			if strings.Contains(err.Error(), "1099511627776") || strings.Contains(err.Error(), "1000000001") {
				t.Errorf("error echoes a value: %v", err)
			}
		})
	}
}

// The rounding problem is joined with the other config errors, one per line,
// each naming its variable.
func TestRateLimitIntervalErrorIsJoinedWithOtherConfigErrors(t *testing.T) {
	cfg := platform.Config{
		APIToken:   "s3cret marker 0123456789abcdef0123",
		ContentDir: t.TempDir() + "/missing-marker-dir",
		RateLimit:  platform.RateLimit{Requests: 2, Per: time.Nanosecond, Burst: 1},
	}
	lines, err := configErrors(t, "joined", cfg)
	if len(lines) != 3 {
		t.Fatalf("err = %q, want three lines", err)
	}
	for _, v := range []string{platform.EnvAPIToken, roundsToZeroLine, platform.EnvContentDir} {
		found := false
		for _, l := range lines {
			found = found || strings.Contains(l, v)
		}
		if !found {
			t.Errorf("err = %q, want a line with %q", err, v)
		}
	}
	for _, hidden := range []string{"s3cret", "missing-marker-dir"} {
		if strings.Contains(err.Error(), hidden) {
			t.Errorf("error echoes a value (%q): %v", hidden, err)
		}
	}
}

// #276: a Start whose context is already dead does not use up the start. A
// later Start with a live context launches the poll loop.
func TestStartWithDeadContextThenLiveContextStillRunsThePollLoop(t *testing.T) {
	waitLoops(t, 0)
	pub := startPublic(t)
	dead, cancelDead := context.WithCancel(context.Background())
	cancelDead()
	startReturns(t, pub, dead, platform.NewRegistry(0))
	waitLoops(t, 0)

	live, cancel := context.WithCancel(context.Background())
	startReturns(t, pub, live, platform.NewRegistry(0))
	waitLoops(t, 1)
	cancel()
	waitLoops(t, 0)
}

// A live Start first, then a second Start (dead or live): one loop, and the
// second call neither starts another nor stops the first.
func TestStartLiveThenSecondStartKeepsOnePollLoop(t *testing.T) {
	for name, second := range map[string]func() (context.Context, context.CancelFunc){
		"dead": func() (context.Context, context.CancelFunc) {
			c, cancel := context.WithCancel(context.Background())
			cancel()
			return c, cancel
		},
		"live": func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
	} {
		t.Run(name, func(t *testing.T) {
			waitLoops(t, 0)
			pub := startPublic(t)
			live, cancel := context.WithCancel(context.Background())
			startReturns(t, pub, live, platform.NewRegistry(0))
			waitLoops(t, 1)
			ctx2, cancel2 := second()
			defer cancel2()
			startReturns(t, pub, ctx2, platform.NewRegistry(0))
			time.Sleep(100 * time.Millisecond) // a wrongly started second loop needs a moment to appear
			if got := pollLoops(); got != 1 {
				t.Fatalf("poll loops after the second Start = %d, want 1", got)
			}
			cancel()
			waitLoops(t, 0)
		})
	}
}
