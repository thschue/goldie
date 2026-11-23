package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Acceptance tests for #416: goldie instructions apply [--target claude|agents]
// [--force] writes or updates the managed block in CLAUDE.md or AGENTS.md at
// the repository root, creating the file if needed, and refuses a hand-edited
// block without --force. goldie instructions check exits non-zero (the command
// returns an error) when the block is missing, outdated or edited, and writes
// nothing. The block comes from GET /api/v1/instructions?target=&project= on a
// fake API on loopback. The tests pin the files and the errors, not the
// wording of the success output.

const instrOrigin = "[remote \"origin\"]\n\turl = git@github.com:Acme/Widgets.git\n"

const (
	instrBlockV1 = "Use goldie before you write code.\nSee goldie_search.\n"
	instrBlockV2 = "Ask goldie first.\nSee goldie_lookup.\n"
	instrNotes   = "# Notes\n\nKeep this line.\n"
	instrAfter   = "\n## After\n\nAlso keep this.\n"
)

// instructionsAPI serves GET /api/v1/instructions to the CLI token. The block
// can be changed while it runs; every request is recorded as
// "<method> <path>?<query>".
type instructionsAPI struct {
	*httptest.Server
	mu    sync.Mutex
	block string
	seen  []string
}

func newInstructionsAPI(t *testing.T, block string) *instructionsAPI {
	t.Helper()
	a := &instructionsAPI{block: block}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.seen = append(a.seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		b := a.block
		a.mu.Unlock()
		target := r.URL.Query().Get("target")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Header.Get("Authorization") != "Bearer "+cliToken:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"missing or wrong token","details":{}}}`))
		case r.Method != http.MethodGet || r.URL.Path != "/api/v1/instructions" || (target != "claude" && target != "agents"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"bad_request","message":"bad request","details":{}}}`))
		default:
			out, _ := json.Marshal(map[string]string{"block": b, "digest": strings.Repeat("0", 64)})
			_, _ = w.Write(out)
		}
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *instructionsAPI) set(block string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.block = block
}

func (a *instructionsAPI) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen...)
}

func runInstr(t *testing.T, api *instructionsAPI, dir string, args ...string) (string, error) {
	t.Helper()
	return runCLIIn(t, dir, append(append([]string{"instructions"}, args...), "--addr", api.URL, "--token", cliToken)...)
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s exists, want nothing written", path)
	} else if !os.IsNotExist(err) {
		t.Errorf("%s: %v", path, err)
	}
}

func TestCLIInstructionsApplyCreatesTheFile(t *testing.T) {
	cases := []struct{ target, file, other string }{
		{"claude", "CLAUDE.md", "AGENTS.md"},
		{"agents", "AGENTS.md", "CLAUDE.md"},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			api := newInstructionsAPI(t, instrBlockV1)
			root := gitRepo(t, instrOrigin)
			out, err := runInstr(t, api, root, "apply", "--target", c.target)
			if err != nil {
				t.Fatalf("apply: %v\n%s", err, out)
			}
			got := fileContent(t, filepath.Join(root, c.file))
			if !strings.Contains(got, "<!-- goldie:begin digest=sha256:") || !strings.Contains(got, "<!-- goldie:end -->") || !strings.Contains(got, instrBlockV1) {
				t.Errorf("%s:\n%s", c.file, got)
			}
			mustNotExist(t, filepath.Join(root, c.other))
			reqs := api.requests()
			if len(reqs) != 1 || !strings.Contains(reqs[0], "target="+c.target) || !strings.Contains(reqs[0], "project=github.com%2Facme%2Fwidgets") {
				t.Errorf("API requests = %q, want one for target %s and the detected Project", reqs, c.target)
			}
		})
	}
}

func TestCLIInstructionsApplyUsesTheProjectFlag(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	if out, err := runInstr(t, api, root, "apply", "--target", "claude", "--project", "github.com/other/thing"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if reqs := api.requests(); len(reqs) != 1 || !strings.Contains(reqs[0], "project=github.com%2Fother%2Fthing") {
		t.Errorf("API requests = %q, want the --project value", reqs)
	}
}

func TestCLIInstructionsApplyKeepsTheRestAndUpdatesTheBlock(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	path := filepath.Join(root, "CLAUDE.md")
	writeFile(t, path, instrNotes)
	if out, err := runInstr(t, api, root, "apply", "--target", "claude"); err != nil {
		t.Fatalf("first apply: %v\n%s", err, out)
	}
	first := fileContent(t, path)
	if !strings.HasPrefix(first, instrNotes) || !strings.Contains(first, instrBlockV1) {
		t.Fatalf("existing text not kept or block missing:\n%s", first)
	}
	// Text a human adds after the block survives an update.
	writeFile(t, path, first+instrAfter)

	// Applying the same block again changes nothing.
	if out, err := runInstr(t, api, root, "apply", "--target", "claude"); err != nil {
		t.Fatalf("second apply: %v\n%s", err, out)
	}
	if got := fileContent(t, path); got != first+instrAfter {
		t.Errorf("a current block was rewritten:\n%s", got)
	}

	api.set(instrBlockV2)
	if out, err := runInstr(t, api, root, "apply", "--target", "claude"); err != nil {
		t.Fatalf("update apply: %v\n%s", err, out)
	}
	got := fileContent(t, path)
	if strings.Contains(got, "goldie_search") || !strings.Contains(got, instrBlockV2) {
		t.Errorf("block not updated:\n%s", got)
	}
	if !strings.HasPrefix(got, instrNotes) || !strings.HasSuffix(got, instrAfter) {
		t.Errorf("text outside the block changed:\n%s", got)
	}
	if strings.Count(got, "<!-- goldie:begin") != 1 || strings.Count(got, "<!-- goldie:end -->") != 1 {
		t.Errorf("want exactly one block:\n%s", got)
	}
}

func TestCLIInstructionsApplyRefusesAHandEditedBlockWithoutForce(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	path := filepath.Join(root, "AGENTS.md")
	writeFile(t, path, instrNotes)
	if out, err := runInstr(t, api, root, "apply", "--target", "agents"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	edited := strings.Replace(fileContent(t, path), "See goldie_search.", "Ignore goldie.", 1) + instrAfter
	writeFile(t, path, edited)
	api.set(instrBlockV2)

	out, err := runInstr(t, api, root, "apply", "--target", "agents")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("apply over an edited block: err = %v, want a refusal that names --force", err)
	}
	if got := fileContent(t, path); got != edited {
		t.Errorf("a refused apply changed the file:\n%s\noutput: %s", got, out)
	}

	if out, err := runInstr(t, api, root, "apply", "--target", "agents", "--force"); err != nil {
		t.Fatalf("apply --force: %v\n%s", err, out)
	}
	got := fileContent(t, path)
	if !strings.Contains(got, instrBlockV2) || strings.Contains(got, "Ignore goldie.") {
		t.Errorf("--force did not replace the block:\n%s", got)
	}
	if !strings.HasPrefix(got, instrNotes) || !strings.HasSuffix(got, instrAfter) {
		t.Errorf("--force changed text outside the block:\n%s", got)
	}
}

func TestCLIInstructionsApplyRefusesADamagedBlockEvenWithForce(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	path := filepath.Join(root, "CLAUDE.md")
	damaged := instrNotes + "<!-- goldie:begin digest=sha256:" + strings.Repeat("a", 64) + " -->\nno end marker\n"
	writeFile(t, path, damaged)
	for _, args := range [][]string{{"apply", "--target", "claude"}, {"apply", "--target", "claude", "--force"}} {
		if _, err := runInstr(t, api, root, args...); err == nil {
			t.Errorf("%v on a damaged block succeeded", args)
		}
		if got := fileContent(t, path); got != damaged {
			t.Errorf("%v changed a damaged file:\n%s", args, got)
		}
	}
}

func TestCLIInstructionsApplyRefusesAnUnknownTarget(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	for _, target := range []string{"gemini", "../CLAUDE", "CLAUDE.md", "Claude", ""} {
		if _, err := runInstr(t, api, root, "apply", "--target", target); err == nil {
			t.Errorf("--target %q succeeded", target)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			t.Errorf("unknown target wrote %s", e.Name())
		}
	}
}

func TestCLIInstructionsApplyWritesNothingWhenTheServerRefuses(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	if _, err := runCLIIn(t, root, "instructions", "apply", "--target", "claude", "--addr", api.URL, "--token", "wrong-token-value"); err == nil {
		t.Error("apply with a rejected token succeeded")
	}
	mustNotExist(t, filepath.Join(root, "CLAUDE.md"))
}

// A block is untrusted Source content. One that could change how the user's
// text after it renders, or that fakes a marker, is refused and nothing is
// written.
func TestCLIInstructionsApplyRefusesBlocksThatChangeHowTheRestRenders(t *testing.T) {
	cases := []struct{ name, block string }{
		{"an open code fence", "Use goldie.\n\n```\nunclosed fence\n"},
		{"an open raw HTML block", "Use goldie.\n\n<div>\nunclosed html\n"},
		{"an open HTML comment", "Use goldie.\n\n<!-- never closed\n"},
		{"a link reference definition", "Use [goldie][g].\n\n[g]: https://evil.example/ \"title\"\n"},
		{"a link reference definition among text", "Use goldie.\n[ref]: https://evil.example/\nMore.\n"},
		{"an end marker", "Use goldie.\n<!-- goldie:end -->\n# After\n"},
		{"a begin marker", "<!-- goldie:begin digest=sha256:" + strings.Repeat("b", 64) + " -->\nUse goldie.\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := newInstructionsAPI(t, c.block)
			root := gitRepo(t, instrOrigin)
			path := filepath.Join(root, "CLAUDE.md")

			// New file: nothing is created.
			if _, err := runInstr(t, api, root, "apply", "--target", "claude"); err == nil {
				t.Error("apply of the block succeeded")
			}
			mustNotExist(t, path)

			// Existing file with an older block and user text after it: unchanged.
			api.set(instrBlockV1)
			if out, err := runInstr(t, api, root, "apply", "--target", "claude"); err != nil {
				t.Fatalf("apply: %v\n%s", err, out)
			}
			before := fileContent(t, path) + instrAfter
			writeFile(t, path, before)
			api.set(c.block)
			for _, args := range [][]string{{"apply", "--target", "claude"}, {"apply", "--target", "claude", "--force"}} {
				if _, err := runInstr(t, api, root, args...); err == nil {
					t.Errorf("%v of the block succeeded", args)
				}
				if got := fileContent(t, path); got != before {
					t.Errorf("%v changed the file:\n%s", args, got)
				}
			}
		})
	}
}

func TestCLIInstructionsApplyLeavesNoTemporaryFiles(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	if out, err := runInstr(t, api, root, "apply", "--target", "claude"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".git" && e.Name() != "CLAUDE.md" {
			t.Errorf("apply left %s in the repository root", e.Name())
		}
	}
}

func TestCLIInstructionsCheck(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	path := filepath.Join(root, "CLAUDE.md")
	check := func() error {
		t.Helper()
		_, err := runInstr(t, api, root, "check", "--target", "claude")
		return err
	}
	unchanged := func(want string) {
		t.Helper()
		if got := fileContent(t, path); got != want {
			t.Errorf("check changed the file:\n%s", got)
		}
	}

	// Missing: no file, then a file without a block. Check creates nothing.
	if err := check(); err == nil {
		t.Error("check with no file succeeded")
	}
	mustNotExist(t, path)
	writeFile(t, path, instrNotes)
	if err := check(); err == nil {
		t.Error("check on a file without the block succeeded")
	}
	unchanged(instrNotes)

	// Current.
	if out, err := runInstr(t, api, root, "apply", "--target", "claude"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	current := fileContent(t, path) + instrAfter
	writeFile(t, path, current)
	if err := check(); err != nil {
		t.Errorf("check on a current block: %v", err)
	}
	unchanged(current)

	// Outdated: the server's block moved on.
	api.set(instrBlockV2)
	if err := check(); err == nil {
		t.Error("check on an outdated block succeeded")
	}
	unchanged(current)

	// Edited by hand.
	api.set(instrBlockV1)
	edited := strings.Replace(current, "See goldie_search.", "Skip goldie.", 1)
	writeFile(t, path, edited)
	if err := check(); err == nil {
		t.Error("check on an edited block succeeded")
	}
	unchanged(edited)
}

func TestCLIInstructionsCheckAsksTheServerForTheProjectAndTarget(t *testing.T) {
	api := newInstructionsAPI(t, instrBlockV1)
	root := gitRepo(t, instrOrigin)
	writeFile(t, filepath.Join(root, "AGENTS.md"), instrNotes)
	_, _ = runInstr(t, api, root, "check", "--target", "agents")
	reqs := api.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0], "target=agents") || !strings.Contains(reqs[0], "project=github.com%2Facme%2Fwidgets") {
		t.Errorf("API requests = %q, want one for target agents and the detected Project", reqs)
	}
}

// The CI snippet, the advice for a CLAUDE.md that imports AGENTS.md and the
// lost-update window live in the docs.
func TestDocsCarryTheInstructionsCISnippetAndTheImportAdvice(t *testing.T) {
	var docs []string
	sep := string(filepath.Separator)
	for _, root := range []string{"README.md", "docs"} {
		_ = filepath.WalkDir(filepath.Join("..", "..", root), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") && !strings.Contains(p, sep+"adr"+sep) {
				if b, rerr := os.ReadFile(p); rerr == nil {
					docs = append(docs, string(b))
				}
			}
			return nil
		})
	}
	var ci, advice, window bool
	for _, d := range docs {
		l := strings.ToLower(d)
		if strings.Contains(d, "goldie instructions check") {
			ci = true
		}
		if strings.Contains(l, "goldie instructions apply") && strings.Contains(d, "@AGENTS.md") &&
			strings.Contains(l, "--target agents") && strings.Contains(l, "twice") {
			advice = true
		}
		if strings.Contains(l, "goldie instructions apply") && (strings.Contains(l, "lost update") || strings.Contains(l, "lost-update")) {
			window = true
		}
	}
	if !ci {
		t.Error("no doc shows the CI snippet: goldie instructions check")
	}
	if !advice {
		t.Error("no doc says to apply only --target agents when CLAUDE.md imports AGENTS.md (@AGENTS.md), so the block is not shown twice")
	}
	if !window {
		t.Error("no doc describing goldie instructions apply documents the lost-update window between read and rename")
	}
}
