package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #274 (slice F of #9): goldie status lists the Project's
// active Suppressions, a lookup of a Suppressed Item fails with a hint naming
// the Suppression and the project.yaml, and goldie validate prints the sync
// warnings (a suppress that names nothing) of a load that still succeeds.
//
// The content is internal/app/testdata/suppress-content: github.com/acme/shop
// suppresses article:platform/notes and article:style (and names
// article:team/gone, which does not exist); github.com/acme/plain suppresses
// nothing.

// suppressCLIServer is the real server over the Suppression content.
func suppressCLIServer(t *testing.T) *httptest.Server {
	t.Helper()
	h, err := app.PublicHandler(platform.Config{ContentDir: "../../internal/app/testdata/suppress-content", APIToken: cliToken, RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestCLIStatusListsTheActiveSuppressionsGolden(t *testing.T) {
	srv := suppressCLIServer(t)
	base := []string{"--addr", srv.URL, "--token", cliToken, "--project", "github.com/acme/shop"}
	for name, args := range map[string][]string{
		"status_suppressions":      {"status"},
		"status_suppressions_json": {"status", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runCLI(t, append(append([]string{}, args...), base...)...)
			if err != nil {
				t.Fatalf("run %v: %v", args, err)
			}
			golden(t, name, out, srv.URL)
		})
	}
}

// The Project is matched ignoring case and detected from a remote like any
// other; status lists what the server holds for it.
func TestCLIStatusListsSuppressionsOfADetectedProject(t *testing.T) {
	srv := suppressCLIServer(t)
	dir := gitRepo(t, remoteCfg("origin", "git@github.com:Acme/Shop.git"))
	out, err := runCLIIn(t, dir, "status", "--addr", srv.URL, "--token", cliToken)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Suppressions: article:platform/notes, article:style\n") {
		t.Errorf("status lacks the Suppressions line:\n%s", out)
	}
}

// Nothing to list means no line, and no key beyond an empty list, so the text
// of a Project without Suppressions (and every existing golden) is unchanged.
// A Project the server does not hold, no Project, a rejected token and no
// token never show Suppressions, and status still exits 0.
func TestCLIStatusWithoutSuppressionsShowsNone(t *testing.T) {
	srv := suppressCLIServer(t)
	cases := []struct {
		name string
		args []string
	}{
		{"a Project without project.yaml", []string{"--token", cliToken, "--project", "github.com/acme/plain"}},
		{"an unregistered Project", []string{"--token", cliToken, "--project", "github.com/acme/none"}},
		{"no Project", []string{"--token", cliToken}},
		{"a rejected token", []string{"--token", "wrong-token-value", "--project", "github.com/acme/shop"}},
		{"no token", []string{"--project", "github.com/acme/shop"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"--addr", srv.URL}, c.args...)
			out, err := runCLI(t, append([]string{"status"}, args...)...)
			if err != nil {
				t.Fatalf("status must exit 0: %v", err)
			}
			if strings.Contains(out, "Suppressions") || strings.Contains(out, "article:") {
				t.Errorf("status shows Suppressions it does not have:\n%s", out)
			}
			m := statusJSON(t, t.TempDir(), args...)
			if list, _ := m["suppressions"].([]any); len(list) != 0 {
				t.Errorf("suppressions = %v, want none", m["suppressions"])
			}
		})
	}
}

// A lookup of a Suppressed Item fails and the error says which Suppression and
// which file hides it, so the developer knows where to change it.
func TestCLILookupOfASuppressedItemNamesTheSuppressionAndFile(t *testing.T) {
	srv := suppressCLIServer(t)
	out, err := runCLI(t, "lookup", "platform/notes", "--addr", srv.URL, "--token", cliToken, "--project", "github.com/acme/shop")
	if err == nil {
		t.Fatalf("lookup of a suppressed Item succeeded:\n%s", out)
	}
	msg := err.Error()
	for _, want := range []string{"Hint:", "article:platform/notes", "projects/github.com/acme/shop/project.yaml"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	if strings.Contains(out, "Platform notes") || strings.Contains(msg, "opts out") {
		t.Errorf("the suppressed Item's content was shown: %q / %q", out, msg)
	}
	// Without the Project the Item is there.
	if out, err := runCLI(t, "lookup", "platform/notes", "--addr", srv.URL, "--token", cliToken); err != nil || !strings.Contains(out, "Platform notes") {
		t.Errorf("lookup without a Project: %q, %v, want the Item", out, err)
	}
}

func TestCLISearchLeavesSuppressedItemsOut(t *testing.T) {
	srv := suppressCLIServer(t)
	out, err := runCLI(t, "search", "kubernetes", "--addr", srv.URL, "--token", cliToken, "--project", "github.com/acme/shop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "platform/deploy") || strings.Contains(out, "platform/notes") || strings.Contains(out, "style") {
		t.Errorf("search for the shop Project:\n%s\nwant platform/deploy only", out)
	}
}

// Hostile Suppression data never reaches the status output as markup or
// control characters: it is printable text on its lines.
func TestCLIStatusSuppressionsHoldNoControlCharacters(t *testing.T) {
	srv := suppressCLIServer(t)
	out, err := runCLI(t, "status", "--addr", srv.URL, "--token", cliToken, "--project", "github.com/acme/shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out {
		if r != '\n' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0)) {
			t.Errorf("status output holds the control character %U", r)
		}
	}
	var m map[string]any
	j, err := runCLI(t, "status", "--json", "--addr", srv.URL, "--token", cliToken, "--project", "github.com/acme/shop")
	if err != nil || json.Unmarshal([]byte(j), &m) != nil {
		t.Fatalf("status --json is not one JSON document: %v\n%s", err, j)
	}
}

// goldie validate prints the warnings of a load that succeeds: exit 0, and a
// line that names the file (relative to the dir) and the reference. It never
// prints a path outside the dir, a stack trace or anything unescaped.
func TestValidatePrintsSyncWarnings(t *testing.T) {
	dir := t.TempDir()
	writeArticle(t, dir, "global/articles/a.md", goodArticle)
	writeArticle(t, dir, "projects/github.com/acme/app/articles/b.md", goodArticle)
	writeArticle(t, dir, "projects/github.com/acme/app/project.yaml", "suppress: [article:a, article:team/gone]\n")
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
	if code != 0 {
		t.Fatalf("exit code %d, want 0: a warning does not fail the load\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	all := stdout + stderr
	for _, want := range []string{"projects/github.com/acme/app/project.yaml", "article:team/gone"} {
		if !strings.Contains(all, want) {
			t.Errorf("validate output lacks %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "article:a,") || strings.Contains(all, "\"article:a\"") {
		t.Errorf("validate warns about a reference that resolves:\n%s", all)
	}
	for _, p := range []string{dir, os.TempDir()} {
		if strings.Contains(all, p) {
			t.Errorf("output echoes the path %q:\n%s", p, all)
		}
	}
	noCrashOutput(t, all)
}

// Valid content with no warning prints no warning.
func TestValidatePrintsNoWarningForCleanSuppressions(t *testing.T) {
	dir := t.TempDir()
	writeArticle(t, dir, "global/articles/a.md", goodArticle)
	writeArticle(t, dir, "projects/github.com/acme/app/articles/b.md", goodArticle)
	writeArticle(t, dir, "projects/github.com/acme/app/project.yaml", "suppress: [article:a]\n")
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
	if code != 0 || strings.TrimSpace(stdout+stderr) != "" {
		t.Errorf("exit %d, output %q %q, want 0 and nothing", code, stdout, stderr)
	}
}

// A reference the Source wrote is shown escaped to printable ASCII and cut,
// each warning on one line of at most 1 KiB, so a Source cannot forge lines or
// flood the output.
func TestValidateWarningsAreEscapedAndBounded(t *testing.T) {
	dir := t.TempDir()
	writeArticle(t, dir, "global/articles/a.md", goodArticle)
	writeArticle(t, dir, "projects/github.com/acme/app/articles/b.md", goodArticle)
	long := "article:" + strings.Repeat("x", 200)
	writeArticle(t, dir, "projects/github.com/acme/app/project.yaml",
		"suppress: [\"article:caf\u00e9\", \""+long+"\"]\n")
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", dir)
	if code != 0 {
		t.Fatalf("exit code %d, want 0: both references are well-formed and only name nothing\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	all := stdout + stderr
	if !strings.Contains(all, "projects/github.com/acme/app/project.yaml") {
		t.Errorf("validate prints no warning for references that name nothing:\n%s", all)
	}
	for _, line := range strings.Split(strings.TrimRight(all, "\n"), "\n") {
		if len(line) > 1024 {
			t.Errorf("a warning line is %d bytes, want at most 1024", len(line))
		}
		for _, b := range []byte(line) {
			if b < 0x20 || b > 0x7e {
				t.Errorf("warning line holds the byte %#x, want printable ASCII: %q", b, line)
				break
			}
		}
	}
	if strings.Contains(all, "caf\u00e9") {
		t.Errorf("the reference is shown unescaped:\n%s", all)
	}
}
