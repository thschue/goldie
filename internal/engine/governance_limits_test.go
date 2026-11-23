package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #269, review round 1: the replaced_by check is part of every Load, and
// a Source is untrusted content, so the check must stay linear in the number
// of Items and its error text must stay bounded whatever a reference holds.

// A Source with many Items that all name the last one must load in about the
// time it takes to parse them. A check that searches the Items once per
// reference takes minutes here (N squared lookups) and holds the poll lock
// for all of it.
func TestReplacedByCheckIsLinearInTheNumberOfItems(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 20000 files")
	}
	const (
		n     = 20000
		bound = 10 * time.Second
	)
	root := t.TempDir()
	// The target sorts after every referrer, so a search in Item order walks
	// all of them before it finds it.
	writeTo(t, root, globalFile("zzzz-target"), article("Target"))
	referrer := govArticle("Referrer", "status: deprecated", "replaced_by: article:zzzz-target")
	for i := range n {
		writeTo(t, root, globalFile(fmt.Sprintf("Team-%02d/Referrer-With-A-Long-Mixed-Case-Name-%05d", i%20, i)), referrer)
	}

	ctx, cancel := context.WithTimeout(t.Context(), bound)
	defer cancel()
	type result struct {
		snap *engine.Snapshot
		err  error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		snap, err := engine.NewDirSource(root).Load(ctx)
		done <- result{snap, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Load after %v: %v", time.Since(start), r.err)
		}
		if got := len(r.snap.Articles()); got != n+1 {
			t.Fatalf("Load returned %d articles, want %d", got, n+1)
		}
		t.Logf("loaded %d Items that name one target in %v", n+1, time.Since(start))
	case <-time.After(bound):
		t.Fatalf("Load of %d Items that name one target did not finish within %v: the replaced_by check is not linear", n+1, bound)
	}
}

// The error quotes the reference, and reaches sync status and logs. A
// reference of a megabyte must not make a megabyte error: the message stays
// short and still names the file.
func TestReplacedByErrorIsBoundedForAHugeReference(t *testing.T) {
	const maxErr = 1 << 10
	// Each value fills most of the 1 MiB an article may hold.
	fill := func(unit string) string { return strings.Repeat(unit, (1<<20-200)/len(unit)) }
	refs := map[string]string{
		// Spaces and colons: rejected when the reference is parsed.
		"malformed": `"article:` + fill("ab cd:") + `"`,
		// Only name characters, naming no Item. Non-ASCII letters are neither
		// a path, an address nor a token, so redaction does not shorten them.
		"dangling": `"article:` + fill("éé-") + `"`,
	}
	for name, ref := range refs {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{globalFile("a"): govArticle("A", "replaced_by: "+ref)}, globalFile("a"))
			if len(msg) > maxErr {
				t.Fatalf("error is %d bytes, want at most %d: %.200q...", len(msg), maxErr, msg)
			}
			if !strings.Contains(msg, "replaced_by") {
				t.Errorf("error %q does not name the field replaced_by", msg)
			}
		})
	}
}

// A reference holds no Unicode space of any kind, and no invisible character:
// a name that reads the same as another must not name a different Item. The
// target exists under the exact spelling, so only the reference rule can fail
// the load.
func TestReplacedByRejectsSpacesAndInvisibleCharactersInTheName(t *testing.T) {
	for name, sep := range map[string]string{
		"plain space":            " ",
		"non-breaking space":     "\u00a0",
		"ideographic space":      "\u3000",
		"em space":               "\u2003",
		"narrow no-break":        "\u202f",
		"zero-width space":       "\u200b",
		"zero-width joiner":      "\u200d",
		"byte order mark":        "\ufeff",
		"right-to-left override": "\u202e",
	} {
		t.Run(name, func(t *testing.T) {
			target := "a" + sep + "b"
			msg := loadErr(t, map[string]string{
				globalFile(target): article("Target"),
				globalFile("ref"):  govArticle("Ref", "status: deprecated", `replaced_by: "article:`+target+`"`),
			}, globalFile("ref"))
			if !strings.Contains(msg, "replaced_by") {
				t.Errorf("error %q does not name the field replaced_by", msg)
			}
			if sep != " " && strings.Contains(msg, sep) {
				t.Errorf("error %q carries the character %+q unescaped", msg, sep)
			}
		})
	}
}

// The negative of the rule above: a name of letters, digits, dashes, dots and
// slashes, in any case, still resolves.
func TestReplacedByAcceptsAnOrdinaryNestedName(t *testing.T) {
	load(t, map[string]string{
		globalFile("team/v1.2_next-gen"): article("Next"),
		globalFile("ref"):                govArticle("Ref", "status: deprecated", "replaced_by: article:Team/V1.2_Next-Gen"),
	})
}
