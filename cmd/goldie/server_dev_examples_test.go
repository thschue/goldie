package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// #451: goldie server -dev on a directory with no content serves the example
// content embedded in the binary and says so once on stderr, naming the
// directory it would read. A directory with content is served as it is: the
// examples are never mixed in.
//
// #682: the embedded set is examples/demo, a neutral demo platform (four
// articles, a skill with a script, an agent and a loop), not goldie's own
// process, which stays in examples/knowledge (devArticle is its article) and
// is served only when --dev is given that directory.

// devExamplesLines are the stderr lines that mention the built-in examples.
func devExamplesLines(p *devProc) []string {
	_, errs := p.lines()
	var got []string
	for _, l := range errs {
		if strings.Contains(strings.ToLower(l), "example") {
			got = append(got, l)
		}
	}
	return got
}

func TestServerDevOnAnEmptyDirectoryServesTheBuiltInExamples(t *testing.T) {
	p := startDev(t, devEnv(), t.TempDir())
	if got := devStatus(t, p.addr, "/api/v1/agents", p.token); got != http.StatusOK {
		t.Errorf("GET /api/v1/agents on an empty directory = %d, want 200 from the built-in examples\n%s", got, p.dump())
	}
	p.stop(t)
	assertTokenOnlyInExportLine(t, p)
}

func TestServerDevOnAnEmptyDirectorySaysSoOnceOnStderrNamingTheDirectory(t *testing.T) {
	dir := t.TempDir()
	p := startDev(t, devEnv(), dir)
	devStatus(t, p.addr, "/api/v1/agents", p.token)
	p.stop(t)
	lines := devExamplesLines(p)
	if len(lines) != 1 {
		t.Fatalf("stderr has %d lines about the built-in examples, want exactly 1: %q\n%s", len(lines), lines, p.dump())
	}
	if !strings.Contains(lines[0], dir) {
		t.Errorf("the examples line does not name the directory %q: %q", dir, lines[0])
	}
	out, _ := p.lines()
	for _, l := range out {
		if strings.Contains(strings.ToLower(l), "example") {
			t.Errorf("the examples line is on stdout, which carries only the export line: %q", l)
		}
	}
}

func TestServerDevOnADirectoryWithContentServesOnlyThatContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "global", "articles"), 0o755); err != nil {
		t.Fatal(err)
	}
	article := "---\nname: My own article\nkeywords: [mine]\n---\nOnly here.\n"
	if err := os.WriteFile(filepath.Join(dir, "global", "articles", "mine.md"), []byte(article), 0o644); err != nil {
		t.Fatal(err)
	}
	p := startDev(t, devEnv(), dir)
	if got := devStatus(t, p.addr, "/api/v1/articles/mine", p.token); got != http.StatusOK {
		t.Errorf("GET the directory's own article = %d, want 200\n%s", got, p.dump())
	}
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusNotFound {
		t.Errorf("GET %s = %d, want 404: the examples must not be mixed into a directory with content", devArticle, got)
	}
	p.stop(t)
	if lines := devExamplesLines(p); len(lines) != 0 {
		t.Errorf("stderr mentions the built-in examples for a directory with content: %q", lines)
	}
}

// devCLI runs `goldie args...` in dir against the --dev server p, with the
// export line's address and token, and returns its stdout. A non-zero exit
// fails the test.
func devCLI(t *testing.T, p *devProc, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goldieBinary(t), args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GOLDIE_ADDR=" + p.addr, "GOLDIE_TOKEN=" + p.token}
	cmd.Dir = dir
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		t.Fatalf("goldie %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, o.String(), e.String())
	}
	return o.String()
}

// devRepo is a bare checkout, where goldie pull works.
func devRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// devItems returns the Items of `goldie <kind> list --json` ("skills",
// "agents" or "loops"), each as its JSON object.
func devItems(t *testing.T, p *devProc, repo, kind string) []map[string]any {
	t.Helper()
	out := devCLI(t, p, repo, kind, "list", "--json")
	var body map[string][]map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("goldie %s list --json is not JSON: %v\n%s", kind, err, out)
	}
	return body[kind]
}

func devNames(items []map[string]any) []string {
	var names []string
	for _, it := range items {
		n, _ := it["name"].(string)
		names = append(names, n)
	}
	return names
}

// goldieOwn returns the names of goldie's own agents or loops: the files of
// examples/knowledge/global/<kind>.
func goldieOwn(t *testing.T, kind string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(contentDir(t), "global", kind))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".md"); ok {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		t.Fatalf("examples/knowledge has no %s: the fixture of this test is gone", kind)
	}
	return names
}

type devSearch struct {
	Results []struct {
		ID string `json:"id"`
	} `json:"results"`
}

func devSearchFor(t *testing.T, p *devProc, repo, q string) devSearch {
	t.Helper()
	out := devCLI(t, p, repo, "search", "--json", q)
	var body devSearch
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("goldie search --json %q is not JSON: %v\n%s", q, err, out)
	}
	return body
}

// Criterion 2: each of the searches a new user is likely to try, the ones the
// UI handoff suggests, finds at least one article in the built-in examples.
func TestServerDevBuiltInExamplesAnswerTheFirstSearchesOfANewUser(t *testing.T) {
	p := startDev(t, devEnv(), t.TempDir())
	repo := devRepo(t)
	for _, q := range []string{"database", "logging", "retries", "feature flags"} {
		if got := devSearchFor(t, p, repo, q); len(got.Results) == 0 {
			t.Errorf("goldie search %q on an empty directory finds no article, want at least one", q)
		}
	}
	// Negative: goldie's own article is not built in.
	for _, r := range devSearchFor(t, p, repo, "agent loops").Results {
		if r.ID == "agent-loops" {
			t.Error("the built-in examples hold goldie's own article agent-loops")
		}
	}
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusNotFound {
		t.Errorf("GET %s on an empty directory = %d, want 404: goldie's own article is not built in", devArticle, got)
	}
}

// Criterion 2: the skills, agents and loops lists each show the demo Item, and
// none of goldie's own. The demo skill is a script skill.
func TestServerDevBuiltInExamplesListADemoSkillAgentAndLoopAndNoneOfGoldiesOwn(t *testing.T) {
	p := startDev(t, devEnv(), t.TempDir())
	repo := devRepo(t)
	skills := devItems(t, p, repo, "skills")
	if len(skills) != 1 {
		t.Errorf("goldie skills list shows %v, want exactly the one demo skill", devNames(skills))
	}
	scripted := false
	for _, sk := range skills {
		if rs, _ := sk["requires_sync"].(bool); rs {
			scripted = true
		}
	}
	if !scripted {
		t.Errorf("no demo skill has a script (requires_sync): %v", devNames(skills))
	}
	for _, kind := range []string{"agents", "loops"} {
		items := devItems(t, p, repo, kind)
		if len(items) != 1 {
			t.Errorf("goldie %s list shows %v, want exactly the one demo Item", kind, devNames(items))
		}
		for _, name := range devNames(items) {
			if slices.Contains(goldieOwn(t, kind), name) {
				t.Errorf("goldie %s list shows %q, one of goldie's own", kind, name)
			}
		}
	}
}

// Criterion 3: goldie pull against the built-in examples writes the demo agent
// and the demo loop's entry skill, and nothing of goldie's own process: no file
// named for one of its agents or loops, and no text that points into the goldie
// repository, which the user's repository does not have.
func TestServerDevPullOfTheBuiltInExamplesWritesTheDemoAndNotGoldiesProcess(t *testing.T) {
	p := startDev(t, devEnv(), t.TempDir())
	repo := devRepo(t)
	devCLI(t, p, repo, "pull")
	var agents, entries, all []string
	err := filepath.WalkDir(filepath.Join(repo, ".claude"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(repo, path)
		rel = filepath.ToSlash(rel)
		all = append(all, rel)
		switch {
		case strings.HasPrefix(rel, ".claude/agents/goldie-") && strings.HasSuffix(rel, ".md"):
			agents = append(agents, rel)
		case strings.HasPrefix(rel, ".claude/skills/goldie-loop-") && strings.HasSuffix(rel, "/SKILL.md"):
			entries = append(entries, rel)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, bad := range []string{"docs/agents", "./run ", "./run`", "thschue/goldie"} {
			if strings.Contains(string(data), bad) {
				t.Errorf("%s mentions %q, which the user's repository does not have", rel, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("goldie pull wrote no .claude directory: %v", err)
	}
	if len(agents) != 1 {
		t.Errorf("goldie pull wrote the agents %v, want exactly the demo agent", agents)
	}
	if len(entries) != 1 {
		t.Errorf("goldie pull wrote the loop entries %v, want exactly the demo loop's", entries)
	}
	for _, own := range goldieOwn(t, "agents") {
		if slices.Contains(all, ".claude/agents/goldie-"+own+".md") {
			t.Errorf("goldie pull wrote goldie's own agent %q", own)
		}
	}
	for _, own := range goldieOwn(t, "loops") {
		if slices.Contains(all, ".claude/skills/goldie-loop-"+own+"/SKILL.md") {
			t.Errorf("goldie pull wrote goldie's own loop %q", own)
		}
	}
}

// Criterion 4: naming examples/knowledge keeps goldie's own agents and loops
// served, and the demo is not mixed in.
func TestServerDevOnExamplesKnowledgeStillServesGoldiesOwnAgentsAndLoops(t *testing.T) {
	p := startDev(t, devEnv(), contentDir(t))
	repo := devRepo(t)
	for _, kind := range []string{"agents", "loops"} {
		got := devNames(devItems(t, p, repo, kind))
		own := goldieOwn(t, kind)
		for _, name := range own {
			if !slices.Contains(got, name) {
				t.Errorf("goldie %s list on examples/knowledge lacks %q: %v", kind, name, got)
			}
		}
		if len(got) != len(own) {
			t.Errorf("goldie %s list on examples/knowledge shows %v, want only goldie's own %v", kind, got, own)
		}
	}
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusOK {
		t.Errorf("GET %s on examples/knowledge = %d, want 200", devArticle, got)
	}
}

// #743: goldie pull keeps the executable bit of a skill file. The built-in
// examples are embedded in the binary, whose file system reports every file as
// read-only, so the bit is easy to lose: the pulled scripts/check.sh must be
// executable, and SKILL.md must stay 0644.
func TestServerDevPullOfTheBuiltInExamplesKeepsTheExecutableBitOfTheSkillScript(t *testing.T) {
	p := startDev(t, devEnv(), t.TempDir())
	repo := devRepo(t)
	devCLI(t, p, repo, "pull")
	skill := filepath.Join(repo, ".claude", "skills", "goldie-service-health")
	check, err := os.Stat(filepath.Join(skill, "scripts", "check.sh"))
	if err != nil {
		t.Fatalf("goldie pull did not write scripts/check.sh: %v", err)
	}
	if check.Mode().Perm() != 0o755 {
		t.Errorf("pulled scripts/check.sh has mode %v, want -rwxr-xr-x", check.Mode().Perm())
	}
	doc, err := os.Stat(filepath.Join(skill, "SKILL.md"))
	if err != nil {
		t.Fatalf("goldie pull did not write SKILL.md: %v", err)
	}
	if doc.Mode().Perm() != 0o644 {
		t.Errorf("pulled SKILL.md has mode %v, want -rw-r--r--", doc.Mode().Perm())
	}
}

// #743: the modes git stores for the example skill files and the modes the
// built-in examples pull with do not drift: a file is executable after the pull
// exactly when git stores it as 100755.
func TestServerDevPulledExampleSkillModesMatchGit(t *testing.T) {
	root := filepath.Join(contentDir(t), "..", "demo")
	cmd := exec.CommandContext(t.Context(), "git", "ls-files", "-s", "--", "global/skills")
	cmd.Dir = root
	listing, err := cmd.Output()
	if err != nil {
		t.Skipf("git does not list the examples here: %v", err)
	}
	gitExec := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
		meta, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		gitExec[strings.TrimPrefix(name, "global/skills/")] = strings.HasPrefix(meta, "100755")
	}
	if len(gitExec) == 0 {
		t.Fatal("git lists no example skill files: the fixture of this test is gone")
	}
	p := startDev(t, devEnv(), t.TempDir())
	repo := devRepo(t)
	devCLI(t, p, repo, "pull")
	for rel, want := range gitExec {
		info, err := os.Stat(filepath.Join(repo, ".claude", "skills", "goldie-"+filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("goldie pull did not write the example skill file %s: %v", rel, err)
			continue
		}
		if got := info.Mode().Perm()&0o111 != 0; got != want {
			t.Errorf("pulled %s: executable = %v, but git stores it with executable = %v", rel, got, want)
		}
	}
}

// #743: a directory Source keeps the executable bit through goldie pull, and
// the files without it stay 0644.
func TestServerDevPullOfADirectorySourceKeepsTheExecutableBit(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "global", "skills", "tool")
	if err := os.MkdirAll(filepath.Join(skill, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: tool\ndescription: Run the tool.\nkeywords: [tool]\n---\nRun scripts/run.sh.\n"
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "scripts", "run.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "scripts", "notes.txt"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	articles := filepath.Join(dir, "global", "articles")
	if err := os.MkdirAll(articles, 0o755); err != nil {
		t.Fatal(err)
	}
	article := "---\nname: Tools\nkeywords: [tool]\n---\nUse the tool.\n"
	if err := os.WriteFile(filepath.Join(articles, "tools.md"), []byte(article), 0o644); err != nil {
		t.Fatal(err)
	}
	p := startDev(t, devEnv(), dir)
	repo := devRepo(t)
	devCLI(t, p, repo, "pull")
	for rel, want := range map[string]os.FileMode{"scripts/run.sh": 0o755, "scripts/notes.txt": 0o644, "SKILL.md": 0o644} {
		info, err := os.Stat(filepath.Join(repo, ".claude", "skills", "goldie-tool", filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("goldie pull did not write %s: %v", rel, err)
			continue
		}
		if info.Mode().Perm() != want {
			t.Errorf("pulled %s has mode %v, want %v", rel, info.Mode().Perm(), want)
		}
	}
}
