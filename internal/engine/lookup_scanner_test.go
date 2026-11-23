package engine_test

import (
	"testing"
)

// Issue #359: placeholder lookup lines are skipped, and the Markdown scanner
// follows CommonMark for fences. Everything is pinned through DirSource.Load.

// loadsWith loads a guide with body beside a published "target" article.
func loadsWith(t *testing.T, body string) {
	t.Helper()
	mustLoad(t, map[string]string{
		globalFile("target"): govArticle("Target"),
		globalFile("guide"):  docWith("Guide", body),
	})
}

// failsWith expects the load to fail on the guide's dangling missing-one.
func failsWith(t *testing.T, body string) {
	t.Helper()
	loadErr(t, map[string]string{
		globalFile("target"): govArticle("Target"),
		globalFile("guide"):  docWith("Guide", body),
	}, globalFile("guide"), "missing-one")
}

// --- Placeholder lookup lines ---

func TestPlaceholderLookupLineIsSkippedNotDangling(t *testing.T) {
	for name, body := range map[string]string{
		"README form, inline": "Run `goldie lookup <id>` to read an article.",
		"README form, fenced": fence("goldie lookup <id>"),
		"ellipsis, inline":    "Run `goldie lookup …` to read an article.",
		"ellipsis, fenced":    fence("goldie lookup …"),
		"tilde fence":         "~~~\ngoldie lookup <id>\n~~~\n",
	} {
		t.Run(name, func(t *testing.T) { loadsWith(t, body) })
	}
}

func TestPlaceholderLineDoesNotHideADanglingLineBesideIt(t *testing.T) {
	failsWith(t, "Run `goldie lookup <id>`, then `goldie lookup missing-one`.")
	failsWith(t, fence("goldie lookup <id>", "goldie lookup missing-one"))
}

func TestRealIdThatDoesNotResolveStaysDangling(t *testing.T) {
	failsWith(t, "Run `goldie lookup missing-one`.")
}

// --- Fence indentation: at most 3 leading spaces (CommonMark) ---

func TestFenceOpenerIndentedFourSpacesIsNotAFence(t *testing.T) {
	// Four spaces make an indented code block, so the backticks open nothing
	// and the next line is plain prose, not a lookup line.
	loadsWith(t, "Intro.\n\n    ```\ngoldie lookup missing-one\n")
	loadsWith(t, "Intro.\n\n\t```\ngoldie lookup missing-one\n")
}

func TestFenceOpenerIndentedUpToThreeSpacesStillOpens(t *testing.T) {
	for _, indent := range []string{"", " ", "  ", "   "} {
		failsWith(t, indent+"```\ngoldie lookup missing-one\n```\n")
		failsWith(t, indent+"~~~\ngoldie lookup missing-one\n~~~\n")
	}
}

func TestClosingFenceIndentedFourSpacesDoesNotClose(t *testing.T) {
	// The indented line is content, so the fence runs on and the later line
	// is still inside it.
	failsWith(t, "```\n    ```\ngoldie lookup missing-one\n```\n")
	failsWith(t, "~~~\n    ~~~\ngoldie lookup missing-one\n~~~\n")
}

func TestClosingFenceIndentedUpToThreeSpacesCloses(t *testing.T) {
	loadsWith(t, "```\ngoldie lookup target\n   ```\ngoldie lookup missing-one\n")
}

func TestShorterClosingFenceDoesNotClose(t *testing.T) {
	failsWith(t, "````\ngoldie lookup target\n```\ngoldie lookup missing-one\n````\n")
	failsWith(t, "~~~~\n~~~\ngoldie lookup missing-one\n~~~~\n")
}

func TestClosingFenceWithTrailingTextDoesNotClose(t *testing.T) {
	failsWith(t, "```\n``` info\ngoldie lookup missing-one\n```\n")
}

func TestCodeSpanInAnIndentedCodeBlockIsNotALookupLine(t *testing.T) {
	loadsWith(t, "Intro.\n\n    run `goldie lookup missing-one` now\n")
	loadsWith(t, "Intro.\n\n\trun `goldie lookup missing-one` now\n")
}

func TestCodeSpanIndentedUpToThreeSpacesIsStillALookupLine(t *testing.T) {
	failsWith(t, "Intro.\n\n   run `goldie lookup missing-one` now\n")
}

func TestFenceInsideAListItemIsScanned(t *testing.T) {
	for name, body := range map[string]string{
		"bullet":         "- step\n  ```sh\n  goldie lookup missing-one\n  ```\n",
		"ordered":        "1. step\n   ```sh\n   goldie lookup missing-one\n   ```\n",
		"nested":         "- a\n  - b\n    ```sh\n    goldie lookup missing-one\n    ```\n",
		"opener on item": "- ```sh\n  goldie lookup missing-one\n  ```\n",
	} {
		t.Run(name, func(t *testing.T) { failsWith(t, body) })
	}
}

func TestResolvingLookupInAFenceInAListItemLoads(t *testing.T) {
	loadsWith(t, "- step\n  ```sh\n  goldie lookup target\n  ```\n")
}

func TestPreHTMLBlockIsExcluded(t *testing.T) {
	for name, body := range map[string]string{
		"plain line":      "<pre>\ngoldie lookup missing-one\n</pre>\n",
		"fence inside":    "<pre>\n```\ngoldie lookup missing-one\n```\n</pre>\n",
		"span inside":     "<pre>\nrun `goldie lookup missing-one`\n</pre>\n",
		"one line":        "<pre>`goldie lookup missing-one`</pre>\n",
		"blank line kept": "<pre>\n\n`goldie lookup missing-one`\n</pre>\n",
	} {
		t.Run(name, func(t *testing.T) { loadsWith(t, body) })
	}
}

func TestScanningResumesAfterThePreBlockEnds(t *testing.T) {
	failsWith(t, "<pre>\nx\n</pre>\n\nRun `goldie lookup missing-one`.\n")
}

func TestUnclosedFenceInsideAPreBlockDoesNotSwallowTheRest(t *testing.T) {
	loadsWith(t, "<pre>\n```\n</pre>\n\nProse only.\n")
}
