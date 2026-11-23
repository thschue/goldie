package engine

import (
	"strings"
	"testing"
	"time"
)

// Issue #359: the lookup scanner has a fuzz target (pattern: FuzzParseRef),
// a worst-case linear-time test and an early return on a line with no
// backtick.

var scannerSeeds = []string{
	"",
	"goldie lookup x",
	"`goldie lookup x`",
	"``goldie lookup x``",
	"`goldie lookup <id>`",
	"```\ngoldie lookup x\n```",
	"~~~\ngoldie lookup x\n~~~",
	"````\n```\ngoldie lookup x\n````",
	"    ```\ngoldie lookup x\n",
	"```\n    ```\ngoldie lookup x\n```",
	"- a\n  ```\n  goldie lookup x\n  ```",
	"<pre>\n```\ngoldie lookup x\n</pre>",
	"`unclosed goldie lookup x",
	"` `` ``` ```` `` `",
	"goldie\tlookup\tx",
	"\r\n```\r\ngoldie lookup x\r\n```\r\n",
	"`goldie lookup \u202ex`",
	"`goldie lookup \x00`",
	"\xff`goldie lookup \xff`",
	strings.Repeat("`", 300),
	"```" + strings.Repeat("`a", 100),
}

// FuzzLookupScan checks that scanning a body never panics, that codeSpans
// returns only substrings of its line (none when the line has no backtick),
// and that a lookup id is a non-empty single word.
func FuzzLookupScan(f *testing.F) {
	for _, s := range scannerSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		for _, id := range lookupIDs(body) {
			if id == "" || strings.ContainsAny(id, " \t\r\n") {
				t.Fatalf("lookupIDs(%q) returned the id %q", body, id)
			}
		}
		for line := range strings.SplitSeq(body, "\n") {
			spans := codeSpans(line)
			if !strings.Contains(line, "`") && len(spans) != 0 {
				t.Fatalf("codeSpans(%q) = %q, want none without a backtick", line, spans)
			}
			for _, s := range spans {
				if !strings.Contains(line, s) {
					t.Fatalf("codeSpans(%q) returned %q, which is not in the line", line, s)
				}
			}
		}
	})
}

func TestCodeSpansOfALineWithoutBacktickReturnsEarly(t *testing.T) {
	line := strings.Repeat("plain prose goldie lookup x ", 40)
	if got := codeSpans(line); len(got) != 0 {
		t.Fatalf("codeSpans = %q, want none", got)
	}
	if n := testing.AllocsPerRun(100, func() { _ = codeSpans(line) }); n != 0 {
		t.Errorf("codeSpans of a line with no backtick allocates %v times, want 0", n)
	}
}

func TestLookupIDsOfBacktickFreeProseDoesNotAllocatePerLine(t *testing.T) {
	body := strings.Repeat("Just prose, no code at all.\n", 20000)
	if n := testing.AllocsPerRun(5, func() { _ = lookupIDs(body) }); n > 8 {
		t.Errorf("lookupIDs allocates %v times for 20000 prose lines, want a small constant", n)
	}
}

// worstCaseBody is close to the 1 MiB body cap and mixes the inputs that make
// a naive scanner quadratic: one line of backtick runs of every length, many
// fences, unterminated spans and lookup lines.
func worstCaseBody() string {
	var b strings.Builder
	for n := 1; b.Len() < 200_000; n++ {
		b.WriteString(strings.Repeat("`", n%200+1))
		b.WriteString(" a ")
	}
	b.WriteString("\n")
	for b.Len() < 400_000 {
		b.WriteString("```\ngoldie lookup a\n````\n~~~\n    ```\n")
	}
	for b.Len() < 600_000 {
		b.WriteString("- x `goldie lookup a` `` ` `` ```\n")
	}
	for b.Len() < 900_000 {
		b.WriteString("`goldie lookup <id>` goldie lookup a\n")
	}
	return b.String()
}

func TestScannerIsLinearOnAWorstCaseBody(t *testing.T) {
	body := worstCaseBody()
	start := time.Now()
	lookupIDs(body)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("lookupIDs took %v on a %d byte worst-case body, want linear time", d, len(body))
	}
	start = time.Now()
	for line := range strings.SplitSeq(body, "\n") {
		codeSpans(line)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("codeSpans over the worst-case lines took %v", d)
	}
}

func TestScannerTimeGrowsLinearlyWithTheBody(t *testing.T) {
	small := worstCaseBody()
	big := small + small + small + small
	best := func(body string) time.Duration {
		fastest := time.Hour
		for range 3 {
			s := time.Now()
			lookupIDs(body)
			if d := time.Since(s); d < fastest {
				fastest = d
			}
		}
		return fastest
	}
	ts, tb := best(small), best(big)
	if ts < time.Millisecond {
		ts = time.Millisecond
	}
	if tb > 12*ts {
		t.Errorf("4x the body took %v against %v, want about 4x (linear)", tb, ts)
	}
}
