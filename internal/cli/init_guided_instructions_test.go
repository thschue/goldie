package cli_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance tests for #800: the guided goldie init offers to apply the
// instruction block to CLAUDE.md. They run at the cli.Run boundary, next to
// init_guided_test.go, and reuse its repository, server and runner.

const (
	applyQuestion = "Add goldie's instruction block to CLAUDE.md? [Y/n]"
	applyLine     = "goldie instructions apply"
	initBlock     = "Ask goldie first.\nSee goldie_lookup.\n"
	blockRoute    = "GET /api/v1/instructions"
	plainCommit   = `git add .goldie/config.yaml && git commit -m "Add goldie config"`
	claudeCommit  = `git add .goldie/config.yaml CLAUDE.md && git commit -m "Add goldie config"`
)

// instructionsServer is a guidedServer that also answers GET
// /api/v1/instructions to the token with initBlock; fail makes that route
// answer 500.
func instructionsServer(t *testing.T, fail bool) *guidedServer {
	t.Helper()
	base := newGuidedServer(t, `"`+guidedID+`"`, false)
	inner := base.Config.Handler
	base.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/instructions" || r.Header.Get("Authorization") != "Bearer "+guidedToken {
			inner.ServeHTTP(w, r)
			return
		}
		base.mu.Lock()
		base.seen = append(base.seen, r.Method+" "+r.URL.Path)
		base.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"boom","message":"the block could not be rendered","details":{}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"block":"Ask goldie first.\nSee goldie_lookup.\n","digest":"00"}`))
	})
	return base
}

func fetchedBlock(srv *guidedServer) bool {
	for _, r := range srv.requests() {
		if r == blockRoute {
			return true
		}
	}
	return false
}

// guidedInstructions runs a guided init in a fresh repository, answering the
// Project, the address and then answer. It returns the root with the outputs.
func guidedInstructions(t *testing.T, srv *guidedServer, claudeMD, answer string) (root, stdout, stderr string, err error) {
	t.Helper()
	root = guidedRepo(t)
	if claudeMD != "" {
		if werr := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(claudeMD), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	stdout, stderr, err = runInit(strings.NewReader("\n"+srv.URL+"\n"+answer), map[string]string{"GOLDIE_TOKEN": guidedToken})
	return root, stdout, stderr, err
}

// standalone runs goldie instructions apply --target claude by itself in the
// current directory against srv, and returns what it printed and its error.
func standalone(t *testing.T, srv *guidedServer) (stdout string, err error) {
	t.Helper()
	env := map[string]string{"GOLDIE_TOKEN": guidedToken, "GOLDIE_ADDR": srv.URL}
	var out strings.Builder
	e := cli.Env{
		Lookup:   func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ReadFile: os.ReadFile,
		Out:      &out,
	}
	err = cli.Run(t.Context(), []string{"instructions", "apply", "--target", "claude"}, e)
	return out.String(), err
}

func claudeFile(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		return ""
	}
	return string(b)
}

func noClaudeFile(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md exists: %v", err)
	}
}

// nextSection is what follows the Next heading on stdout.
func nextSection(s string) string {
	_, after, _ := strings.Cut(s, "\nNext:\n")
	return after
}

// 1. The question is asked when the server is reachable and the token is
// accepted; otherwise there is no prompt and Next keeps the line.
func TestInitAsksToAddTheInstructionBlock(t *testing.T) {
	srv := instructionsServer(t, false)
	_, stdout, stderr, err := guidedInstructions(t, srv, "", "n\n")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stderr, applyQuestion) {
		t.Fatalf("stderr lacks the question:\n%s", stderr)
	}
	if strings.Contains(stdout, applyQuestion) {
		t.Errorf("the question is on stdout:\n%s", stdout)
	}
	if i, j := strings.Index(stderr, "Token: accepted"), strings.Index(stderr, applyQuestion); i < 0 || j < i {
		t.Errorf("the question does not come after the Token line:\n%s", stderr)
	}
	if !hasLine(stdout, "Registered: yes") || !hasLine(nextSection(stdout), applyLine) {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestInitDoesNotAskWithoutAServerAndAnAcceptedToken(t *testing.T) {
	cases := map[string]struct {
		token  string
		server bool
	}{
		"token rejected": {"wrong-token", true},
		"token not set":  {"", true},
		"no server":      {guidedToken, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := guidedRepo(t)
			env := map[string]string{}
			if c.token != "" {
				env["GOLDIE_TOKEN"] = c.token
			}
			srv := instructionsServer(t, false)
			answers := "\n\n"
			if c.server {
				answers = "\n" + srv.URL + "\n"
			}
			stdout, stderr, err := runInit(strings.NewReader(answers+"y\n"), env)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			if strings.Contains(stdout+stderr, "instruction block to CLAUDE.md") {
				t.Errorf("init asked about the instruction block:\nstdout: %s\nstderr: %s", stdout, stderr)
			}
			if fetchedBlock(srv) {
				t.Errorf("init fetched the block without a prompt: %v", srv.requests())
			}
			noClaudeFile(t, root)
			if c.server && !hasLine(nextSection(stdout), applyLine) {
				t.Errorf("Next lacks %q:\n%s", applyLine, stdout)
			}
			if c.server && !hasLine(nextSection(stdout), plainCommit) {
				t.Errorf("Next lacks the plain commit line:\n%s", stdout)
			}
		})
	}
}

// 2. Yes runs goldie instructions apply --target claude and prints its
// messages unchanged; Next drops the line.
func TestInitYesAppliesTheInstructionBlock(t *testing.T) {
	for name, answer := range map[string]string{"Enter": "\n", "y": "y\n"} {
		t.Run(name, func(t *testing.T) {
			srv := instructionsServer(t, false)
			root, stdout, _, err := guidedInstructions(t, srv, "# Notes\n\nKeep this line.\n", answer)
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			const msg = "Wrote the goldie block to CLAUDE.md (was missing).\n"
			if !strings.Contains(stdout, msg) {
				t.Errorf("stdout lacks the command's message %q:\n%s", msg, stdout)
			}
			got := claudeFile(root)
			if !strings.HasPrefix(got, "# Notes\n\nKeep this line.\n") || !strings.Contains(got, initBlock) {
				t.Errorf("CLAUDE.md:\n%s", got)
			}
			next := nextSection(stdout)
			if hasLine(next, plainCommit) {
				t.Errorf("Next commits the config without the CLAUDE.md init wrote:\n%s", next)
			}
			if next == "" || strings.Contains(next, applyLine) {
				t.Errorf("Next still has %q, or is missing:\n%s", applyLine, stdout)
			}
			for _, keep := range []string{"goldie status", claudeCommit} {
				if !hasLine(next, keep) {
					t.Errorf("Next lost %q:\n%s", keep, next)
				}
			}
		})
	}
}

// 3. An already current block prints the command's message; a hand-edited
// block is never forced.
func TestInitWithACurrentBlockSaysSo(t *testing.T) {
	file, _, err := replaceBlock("# Notes\n", initBlock, false)
	if err != nil {
		t.Fatal(err)
	}
	srv := instructionsServer(t, false)
	root, stdout, _, err := guidedInstructions(t, srv, file, "y\n")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stdout, "CLAUDE.md is already up to date.\n") {
		t.Errorf("stdout lacks the already-current message:\n%s", stdout)
	}
	if got := claudeFile(root); got != file {
		t.Errorf("CLAUDE.md changed:\n%s", got)
	}
	if strings.Contains(nextSection(stdout), applyLine) {
		t.Errorf("Next keeps %q for a current block:\n%s", applyLine, stdout)
	}
}

func TestInitNeverForcesAHandEditedBlock(t *testing.T) {
	file, _, err := replaceBlock("# Notes\n", initBlock, false)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(file, "See goldie_lookup.", "Skip goldie, it is slow.", 1)
	srv := instructionsServer(t, false)
	root, stdout, stderr, _ := guidedInstructions(t, srv, edited, "y\n")

	if got := claudeFile(root); got != edited {
		t.Errorf("CLAUDE.md was changed or forced:\n%s", got)
	}
	// The command's own message carries the hint for --force.
	guidedRepo(t)
	if err := os.WriteFile("CLAUDE.md", []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	_, want := standalone(t, srv)
	if !errors.Is(want, cli.ErrBlockEdited) {
		t.Fatalf("the command alone: %v, want ErrBlockEdited", want)
	}
	if !strings.Contains(want.Error(), "goldie instructions apply --target claude --force") {
		t.Errorf("the command's hint changed: %v", want)
	}
	if !strings.Contains(stdout+stderr, want.Error()) {
		t.Errorf("the command's message %q is not printed:\nstdout: %s\nstderr: %s", want.Error(), stdout, stderr)
	}
	if !wroteConfig(t, root) {
		t.Error("config.yaml is gone")
	}
	if !hasLine(nextSection(stdout), applyLine) {
		t.Errorf("Next dropped %q for an edited block:\n%s", applyLine, stdout)
	}
}

// 4. A failing step keeps the config, reports the error, prints Next with the
// retry line and exits 1.
func TestInitKeepsTheConfigWhenApplyingFails(t *testing.T) {
	srv := instructionsServer(t, true)
	root, stdout, stderr, err := guidedInstructions(t, srv, "", "y\n")
	if err == nil {
		t.Fatal("init succeeded; want exit 1")
	}
	var usage *cli.UsageError
	if errors.As(err, &usage) {
		t.Errorf("the failure is a usage error (exit 2): %v", err)
	}
	if !strings.Contains(err.Error()+stderr, "the block could not be rendered") {
		t.Errorf("the error does not say why:\nerr: %v\nstderr: %s", err, stderr)
	}
	if strings.Contains(stdout, "the block could not be rendered") {
		t.Errorf("the error is on stdout:\n%s", stdout)
	}
	if !wroteConfig(t, root) {
		t.Error("config.yaml is gone")
	}
	noClaudeFile(t, root)
	if !hasLine(nextSection(stdout), applyLine) {
		t.Errorf("Next lacks %q to retry:\n%s", applyLine, stdout)
	}
}

// 5. No answer or n changes nothing; non-interactive output is unchanged.
func TestInitDecliningTheInstructionBlockChangesNothing(t *testing.T) {
	for name, answer := range map[string]string{"n": "n\n", "no answer": ""} {
		t.Run(name, func(t *testing.T) {
			srv := instructionsServer(t, false)
			root, stdout, _, _ := guidedInstructions(t, srv, "", answer)
			noClaudeFile(t, root)
			if !wroteConfig(t, root) {
				t.Error("config.yaml is gone")
			}
			if fetchedBlock(srv) {
				t.Errorf("the block was fetched: %v", srv.requests())
			}
			if !hasLine(nextSection(stdout), applyLine) || !hasLine(nextSection(stdout), plainCommit) {
				t.Errorf("Next lacks %q or the plain commit line:\n%s", applyLine, stdout)
			}
		})
	}
}

func TestInitWithoutATerminalNeverTouchesCLAUDEmd(t *testing.T) {
	srv := instructionsServer(t, false)
	root := guidedRepo(t)
	stdout, stderr, err := runInit(nil, map[string]string{"GOLDIE_TOKEN": guidedToken}, "--addr", srv.URL)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if strings.Contains(stdout+stderr, "instruction block") || stderr != "" {
		t.Errorf("non-interactive output changed:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	noClaudeFile(t, root)
}
