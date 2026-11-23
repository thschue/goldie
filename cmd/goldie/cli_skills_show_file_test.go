package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Acceptance tests for #538: goldie skills show <name> --file <path> writes the
// file's raw bytes to stdout and nothing else, and an unknown path is an error
// on stderr with a next step, exit 1. The CLI asks GET
// /api/v1/skills/{name}/files/{path} (getSkillFile) with the path as one
// percent-encoded segment (ADR 0016). Pattern: cli_skills_agents_test.go
// (#409) and lookup.

// sfFiles records the requests a stub skill-file API receives.
type sfFiles struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *sfFiles) requests() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.reqs...)
}

// sfServer answers the exact escaped paths in files with their bytes (200,
// octet-stream) and every other path with the not_found envelope carrying
// missHint.
func sfServer(t *testing.T, files map[string][]byte, missHint string) (string, *sfFiles) {
	t.Helper()
	rec := &sfFiles{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, r.Clone(r.Context()))
		rec.mu.Unlock()
		if body, ok := files[r.URL.EscapedPath()]; ok {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such skill file","details":{"hint":` + jsonString(t, missHint) + `}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, rec
}

const sfDeployFile = "/api/v1/skills/deploy/files/"

// runCLIStderr is runCLI plus what main would print to stderr and the exit
// code it would return for the error.
func runCLIStderr(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	stdout, err := runCLI(t, args...)
	var errBuf bytes.Buffer
	code = exitCode(err, &errBuf)
	return stdout, errBuf.String(), code
}

func TestCLISkillsShowFilePrintsTheRawBytesAndNothingElse(t *testing.T) {
	// Binary: NUL, invalid UTF-8, a terminal escape, a bidi override, no final
	// newline. Bytes in are bytes out: no cleaning, no added newline.
	binary := []byte("#!/bin/sh\x00\xff\xfe\x1b[2J\u202e\nno newline at the end")
	addr, rec := sfServer(t, map[string][]byte{
		sfDeployFile + "SKILL.md":         []byte("# Deploy\n"),
		sfDeployFile + "scripts%2Frun.sh": binary,
		sfDeployFile + "empty.txt":        {},
	}, "x")
	files := []string{"SKILL.md", "scripts/run.sh", "empty.txt"}
	wants := [][]byte{[]byte("# Deploy\n"), binary, {}}
	for i, file := range files {
		out, stderr, code := runCLIStderr(t, "skills", "show", "deploy", "--file", file, "--addr", addr, "--token", cliToken)
		if code != 0 || stderr != "" {
			t.Errorf("--file %s: exit %d, stderr %q, want 0 and empty", file, code, stderr)
		}
		if out != string(wants[i]) {
			t.Errorf("--file %s: stdout %q, want exactly the file's bytes %q", file, out, wants[i])
		}
	}
	reqs := rec.requests()
	if len(reqs) != 3 {
		t.Fatalf("made %d requests, want one GET per file", len(reqs))
	}
	for _, r := range reqs {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+cliToken {
			t.Errorf("%s %s sent Authorization %q, want a GET with the bearer token", r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization"))
		}
	}
	// A nested path is one encoded segment, as the contract says.
	if got := reqs[1].URL.EscapedPath(); got != sfDeployFile+"scripts%2Frun.sh" {
		t.Errorf("scripts/run.sh requested %q, want scripts%%2Frun.sh as one segment", got)
	}
}

func TestCLISkillsShowFilePrintsALargeFileWhole(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789abcdef\n"), 64*1024) // about 1 MiB
	addr, _ := sfServer(t, map[string][]byte{sfDeployFile + "big.txt": big}, "x")
	out, err := runCLI(t, "skills", "show", "deploy", "--file", "big.txt", "--addr", addr, "--token", cliToken)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(big) {
		t.Errorf("stdout is %d bytes, want the file's %d bytes unchanged", len(out), len(big))
	}
}

func TestCLISkillsShowFileReadsTheExampleContent(t *testing.T) {
	// Resolve the relative content path before runCLI changes the working directory.
	dir := dirContent(t)
	want, err := os.ReadFile(filepath.Join(dir, "global", "skills", "deploy", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	srv := cliServer(t)
	remote, stderr, code := runCLIStderr(t, "skills", "show", "deploy", "--file", "SKILL.md", "--addr", srv.URL, "--token", cliToken)
	if code != 0 || stderr != "" || remote != string(want) {
		t.Errorf("remote: exit %d, stderr %q, stdout %q, want 0, empty and the file's bytes %q", code, stderr, remote, want)
	}
	// Local mode over the same content prints the same bytes.
	local, stderr, code := runCLIStderr(t, "skills", "show", "deploy", "--file", "SKILL.md", "--dir", dir)
	if code != 0 || stderr != "" || local != string(want) {
		t.Errorf("--dir: exit %d, stderr %q, stdout %q, want 0, empty and the file's bytes %q", code, stderr, local, want)
	}
}

func TestCLISkillsShowFileOfAnUnknownPathIsAnErrorWithANextStep(t *testing.T) {
	addr, _ := sfServer(t, map[string][]byte{sfDeployFile + "SKILL.md": []byte("# Deploy\n")}, "List the skills, read the one you want, then use a path from its files list.")
	out, stderr, code := runCLIStderr(t, "skills", "show", "deploy", "--file", "missing.txt", "--addr", addr, "--token", cliToken)
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if out != "" {
		t.Errorf("stdout %q on a miss, want nothing", out)
	}
	// The next step lists the skill's files: goldie skills show <name>.
	requireContains(t, "stderr", stderr, "goldie skills show deploy")
	for _, bad := range []string{addr, strings.TrimPrefix(addr, "http://"), cliToken} {
		if strings.Contains(stderr, bad) {
			t.Errorf("stderr carries %q: %q", bad, stderr)
		}
	}
	// An unknown skill is the same kind of miss.
	out, _, code = runCLIStderr(t, "skills", "show", "nope", "--file", "SKILL.md", "--addr", addr, "--token", cliToken)
	if code != 1 || out != "" {
		t.Errorf("unknown skill: exit %d, stdout %q, want 1 and nothing on stdout", code, out)
	}
}

func TestCLISkillsShowFileMissInLocalModeIsTheSameError(t *testing.T) {
	out, stderr, code := runCLIStderr(t, "skills", "show", "deploy", "--file", "missing.txt", "--dir", dirContent(t))
	if code != 1 || out != "" {
		t.Errorf("exit %d, stdout %q, want 1 and nothing on stdout", code, out)
	}
	requireContains(t, "stderr", stderr, "goldie skills show deploy")
}

// The path always travels as one escaped segment, so traversal, encoded
// separators and control characters cannot reach another route or smuggle a
// header or a query; the server then answers a miss.
func TestCLISkillsShowFileKeepsTheHostilePathOneSegment(t *testing.T) {
	// Control: a plain path is requested, so the checks below are about the
	// command, not an absent flag.
	ctlAddr, ctl := sfServer(t, map[string][]byte{sfDeployFile + "SKILL.md": []byte("ok")}, "x")
	if out, err := runCLI(t, "skills", "show", "deploy", "--file", "SKILL.md", "--addr", ctlAddr, "--token", cliToken); err != nil || out != "ok" {
		t.Fatalf("control: out %q, err %v, want ok", out, err)
	}
	if n := len(ctl.requests()); n != 1 {
		t.Fatalf("control made %d requests, want 1", n)
	}
	paths := []string{
		"../SKILL.md",
		"../../agents/reviewer",
		"scripts/../../x",
		"/etc/passwd",
		"..%2F..%2Fetc%2Fpasswd",
		"a%2Fb",
		"a%00b",
		"a\x00b",
		"a\nb",
		"a\r\nX-Evil: 1",
		"a\x1b[2Jb",
		"a\u202eb",
		"x?project=evil",
		"x#frag",
		"a b",
		`a\b`,
	}
	for _, p := range paths {
		addr, rec := sfServer(t, nil, "x")
		out, stderr, _ := runCLIStderr(t, "skills", "show", "deploy", "--file", p, "--addr", addr, "--token", cliToken)
		if out != "" {
			t.Errorf("--file %q: stdout %q, want nothing on a miss", p, out)
		}
		if strings.ContainsAny(stderr, "\x00\x1b\u202e\r") {
			t.Errorf("--file %q: stderr carries a control or format character: %q", p, stderr)
		}
		for _, r := range rec.requests() {
			if want := sfDeployFile + url.PathEscape(p); r.URL.EscapedPath() != want {
				t.Errorf("--file %q requested %q, want %q", p, r.URL.EscapedPath(), want)
			}
			if r.URL.RawQuery != "" || r.Header.Get("X-Evil") != "" {
				t.Errorf("--file %q smuggled the query %q or header %q", p, r.URL.RawQuery, r.Header.Get("X-Evil"))
			}
		}
	}
}

// An empty or dot-only path is wrong use, refused before any request, as an
// empty or dot-only name is.
func TestCLISkillsShowFileRefusesAnEmptyOrDotOnlyPathBeforeAnyRequest(t *testing.T) {
	ctlAddr, _ := sfServer(t, map[string][]byte{sfDeployFile + "SKILL.md": []byte("ok")}, "x")
	if out, err := runCLI(t, "skills", "show", "deploy", "--file", "SKILL.md", "--addr", ctlAddr, "--token", cliToken); err != nil || out != "ok" {
		t.Fatalf("control: out %q, err %v, want ok", out, err)
	}
	for _, p := range []string{"", ".", ".."} {
		addr, rec := sfServer(t, map[string][]byte{sfDeployFile + "SKILL.md": []byte("ok")}, "x")
		out, stderr, code := runCLIStderr(t, "skills", "show", "deploy", "--file", p, "--addr", addr, "--token", cliToken)
		if code != 2 || out != "" || !strings.Contains(stderr, "goldie skills --help") {
			t.Errorf("--file %q: exit %d, stdout %q, stderr %q, want exit 2 and the usage next step", p, code, out, stderr)
		}
		if n := len(rec.requests()); n != 0 {
			t.Errorf("--file %q made %d requests, want none", p, n)
		}
	}
}

func TestCLISkillsShowFileMisuseMakesNoRequest(t *testing.T) {
	addr, rec := sfServer(t, map[string][]byte{sfDeployFile + "SKILL.md": []byte("ok")}, "x")
	for _, args := range [][]string{
		{"skills", "show", "--file", "SKILL.md"},                       // no name
		{"skills", "show", "deploy", "--file"},                         // no value
		{"skills", "show", "deploy", "extra", "--file", "SKILL.md"},    // two names
		{"skills", "list", "--file", "SKILL.md"},                       // not a list flag
		{"agents", "show", "reviewer", "--file", "SKILL.md"},           // agents have no files
		{"skills", "show", "../agents/reviewer", "--file", "SKILL.md"}, // a name is one segment
	} {
		out, err := runCLI(t, append(args, "--addr", addr, "--token", cliToken)...)
		if err == nil {
			t.Errorf("%v must fail", args)
		}
		if strings.Contains(out, "ok") {
			t.Errorf("%v printed file bytes: %q", args, out)
		}
	}
	if n := len(rec.requests()); n != 0 {
		t.Errorf("wrong use made %d requests, want none", n)
	}
	// Control: right use of the same command asks the server.
	if out, err := runCLI(t, "skills", "show", "deploy", "--file", "SKILL.md", "--addr", addr, "--token", cliToken); err != nil || out != "ok" {
		t.Errorf("control: out %q, err %v, want ok", out, err)
	}
}

func TestCLISkillsShowFileWithoutATokenFailsBeforeAnyRequest(t *testing.T) {
	addr, rec := sfServer(t, map[string][]byte{sfDeployFile + "SKILL.md": []byte("ok")}, "x")
	out, err := runCLI(t, "skills", "show", "deploy", "--file", "SKILL.md", "--addr", addr)
	if err == nil || !strings.Contains(err.Error(), "no token") || out != "" {
		t.Errorf("out %q, err %v, want the no-token error and no output", out, err)
	}
	if n := len(rec.requests()); n != 0 {
		t.Errorf("made %d requests without a token, want none", n)
	}
}

func TestCLISkillsShowFileAsksInTheProject(t *testing.T) {
	addr, rec := sfServer(t, map[string][]byte{sfDeployFile + "SKILL.md": []byte("ok")}, "x")
	const project = "github.com/acme/shop"
	if _, err := runCLI(t, "skills", "show", "deploy", "--file", "SKILL.md", "--project", project, "--addr", addr, "--token", cliToken); err != nil {
		t.Fatal(err)
	}
	reqs := rec.requests()
	if len(reqs) != 1 || reqs[0].URL.Query().Get("project") != project {
		t.Errorf("requests %v, want one asking project=%q", reqs, project)
	}
}

// A server's miss text is untrusted: format characters and terminal escapes
// never reach stderr.
func TestCLISkillsShowFileMissDropsFormatCharactersAndEscapesFromServerText(t *testing.T) {
	addr, _ := sfServer(t, nil, "hint "+hiddenCf+"\x1b[2J\u202e\nFORGED: line")
	out, stderr, code := runCLIStderr(t, "skills", "show", "deploy", "--file", "x", "--addr", addr, "--token", cliToken)
	if code != 1 || out != "" {
		t.Errorf("exit %d, stdout %q, want 1 and nothing on stdout", code, out)
	}
	if leaked := formatChars(stderr); len(leaked) > 0 || strings.Contains(stderr, "\x1b") {
		t.Errorf("stderr carries format characters %v or an escape: %q", leaked, stderr)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "FORGED") {
			t.Errorf("server text starts a line of its own: %q", stderr)
		}
	}
}

// Criterion: README.md says what goldie skills and goldie agents read in --dir
// mode, which directories, and mentions --file; the --dir sentence is split so
// each sentence makes one point.
func TestREADMEDocumentsSkillsShowFileAndWhatDirModeReads(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)
	if strings.Contains(readme, "`show --file` is not available yet") {
		t.Error("README.md still says show --file is not available yet")
	}
	if !strings.Contains(readme, "--file") {
		t.Error("README.md does not mention --file")
	}
	var para string
	for _, p := range strings.Split(readme, "\n\n") {
		if strings.Contains(p, "`--dir PATH`") && strings.Contains(p, "embedded server") {
			para = p
		}
	}
	if para == "" {
		t.Fatal("README.md has no paragraph on --dir mode")
	}
	for _, dir := range []string{"global/skills", "global/agents"} {
		if !strings.Contains(para, dir) {
			t.Errorf("the --dir paragraph does not name %s, which skills and agents read there:\n%s", dir, para)
		}
	}
	flat := strings.Join(strings.Fields(para), " ")
	for _, s := range regexp.MustCompile(`[.!?] `).Split(flat, -1) {
		if strings.Contains(s, "`--dir` overrides") && strings.Contains(s, ";") {
			t.Errorf("the --dir sentence still makes more than one point: %q", s)
		}
	}
}
