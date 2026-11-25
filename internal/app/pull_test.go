package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #16 (goldie pull: materialise the Goldie namespace), at
// the HTTP contract seam (GET /api/v1/bundle) and the CLI boundary (goldie
// pull in temporary project directories, against the real public handler).
// The pattern followed is internal/app/skills_agents_api_test.go for the HTTP
// side (fixture, tokens, do and its spec check) and
// internal/app/mcp_stdio_project_test.go for the temporary checkouts
// (gitCheckout, outsideRepository); the command is run as
// sanitise_drift_test.go's runCLI runs one: cli.Command plus pflag.
//
// The contract they fix, beyond the issue:
//   - GET /api/v1/bundle?project=&target=claude is a gzip-compressed tar of
//     the resolved set in the Project's view (the view of /api/v1/skills and
//     /api/v1/agents: nearest Scope wins, drafts left out). Its entries are
//     regular files with clean slash paths relative to the repository root:
//     .claude/skills/goldie-<name>/<path in the bundle> for each file of each
//     skill and .claude/agents/goldie-<name>.md for each agent;
//   - the ETag is the digest of the set: the same on every request for the
//     same set, different for a different set, and an If-None-Match that
//     carries it is a 304 with no body;
//   - an unknown target is a 400 bad_request envelope, as an invalid project is;
//   - goldie pull [--target claude] [--dry-run] [--check] works on the
//     repository root of the working directory and detects the Project as
//     every client command does (--project here);
//   - the Goldie namespace is .claude/skills/goldie-* and
//     .claude/agents/goldie-*. A pull replaces it wholesale and changes
//     nothing else, except for the digest stamp, which may live under .goldie/
//     or in a path under .claude/ whose first element starts with "goldie";
//   - a pull whose archive holds anything unsafe writes nothing at all.

// pullProject is the Project of the fixture that has its own deploy bundle and
// reviewer.
const pullProject = itemsProject

const (
	bundleAPI = "/api/v1/bundle"

	// Where each file of the fixture lands, relative to the repository root.
	pDeploySkill   = ".claude/skills/goldie-deploy/SKILL.md"
	pDeployList    = ".claude/skills/goldie-deploy/checklist.txt"
	pDeployScript  = ".claude/skills/goldie-deploy/scripts/run.sh"
	pLintSkill     = ".claude/skills/goldie-lint/SKILL.md"
	pPlannerAgent  = ".claude/agents/goldie-planner.md"
	pReviewerAgent = ".claude/agents/goldie-reviewer.md"
)

// fileSet is a path to content map.
type fileSet map[string]string

func (s fileSet) paths() []string {
	p := make([]string, 0, len(s))
	for k := range s {
		p = append(p, k)
	}
	sort.Strings(p)
	return p
}

func absContent(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(skillsAgentsContent)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// pullServer is the real public handler over the content dir.
func pullServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(activityHandler(t, platform.Config{ContentDir: content, RateLimit: roomyRateLimit}))
	t.Cleanup(srv.Close)
	return srv
}

// copyContent copies the fixture into a temporary directory, modes kept, so a
// test can change it.
func copyContent(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := absContent(t)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// --- the archive ---

type tarEntry struct {
	name, link string
	typ        byte // 0 is a regular file
	mode       int64
	body       string
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ, mode := e.typ, e.mode
		if typ == 0 {
			typ = tar.TypeReg
		}
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: typ, Mode: mode}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type bundleFile struct {
	mode int64
	data string
}

// readBundle unpacks a bundle body, failing the test unless every entry is a
// directory or a regular file with a clean relative slash path.
func readBundle(t *testing.T, body []byte) map[string]bundleFile {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("the bundle is not gzip: %v (first bytes %q)", err, firstBytes(body))
	}
	tr := tar.NewReader(gz)
	out := map[string]bundleFile{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("the bundle is not a tar: %v", err)
		}
		name := strings.TrimSuffix(h.Name, "/")
		if strings.HasPrefix(h.Name, "/") || slices.Contains(strings.Split(name, "/"), "..") || strings.Contains(h.Name, "\\") {
			t.Errorf("the bundle holds the unsafe path %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg:
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			out[name] = bundleFile{mode: h.Mode, data: string(data)}
		default:
			t.Errorf("the bundle holds %q of type %q, want regular files only", h.Name, h.Typeflag)
		}
	}
}

func firstBytes(b []byte) []byte {
	if len(b) > 40 {
		return b[:40]
	}
	return b
}

func bundleContents(m map[string]bundleFile) fileSet {
	out := fileSet{}
	for k, v := range m {
		out[k] = v.data
	}
	return out
}

// bundleGet is an authenticated GET of the bundle for project ("" for none)
// and target, with mutate applied; the response is checked against the spec.
func bundleGet(t *testing.T, srv *httptest.Server, project, target string, mutate func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	q := url.Values{"target": {target}}
	if project != "" {
		q.Set("project", project)
	}
	return do(t, http.MethodGet, srv.URL+bundleAPI+"?"+q.Encode(), func(r *http.Request) {
		bearer(readerToken)(r)
		if mutate != nil {
			mutate(r)
		}
	})
}

func wantBundleOK(t *testing.T, resp *http.Response, body []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET bundle: status %d, want 200: %s", resp.StatusCode, body)
	}
}

// The expected sets of the fixture for the claude target.
func shopSet() fileSet {
	return fileSet{
		pDeploySkill: "# Deploy shop\n",
		pDeployList:  "check it\n",
		pLintSkill:   "# Lint\n",
	}
}

func globalSet() fileSet {
	return fileSet{
		pDeploySkill:  "# Deploy global\n",
		pDeployScript: "#!/bin/sh\necho hi\n",
		pLintSkill:    "# Lint\n",
	}
}

// wantSet checks the skills of got exactly, and that each agent file is there
// with the words of its source (the wording around them is the adapter's).
func wantSet(t *testing.T, label string, got fileSet, skills fileSet, agents map[string][]string) {
	t.Helper()
	var wantPaths []string
	for _, p := range skills.paths() {
		wantPaths = append(wantPaths, p)
		if got[p] != skills[p] {
			t.Errorf("%s: %s is %q, want %q", label, p, got[p], skills[p])
		}
	}
	for p, words := range agents {
		wantPaths = append(wantPaths, p)
		body, ok := got[p]
		if !ok {
			t.Errorf("%s: %s is missing", label, p)
		}
		for _, w := range words {
			if !strings.Contains(body, w) {
				t.Errorf("%s: %s lacks %q: %q", label, p, w, body)
			}
		}
	}
	sort.Strings(wantPaths)
	if have := got.paths(); !slices.Equal(have, wantPaths) {
		t.Errorf("%s: files %v, want exactly %v", label, have, wantPaths)
	}
}

var (
	shopAgents   = map[string][]string{pPlannerAgent: {"You are planner."}, pReviewerAgent: {"Reviews shop code", "You are the shop reviewer."}}
	globalAgents = map[string][]string{pPlannerAgent: {"You are planner."}, pReviewerAgent: {"Reviews code globally", "You are reviewer."}}
)

// --- HTTP contract ---

// Criterion: the served spec declares GET /api/v1/bundle.
func TestServedSpecDeclaresTheBundleOperation(t *testing.T) {
	if ops := specOperations(t); !slices.Contains(ops, "GET "+bundleAPI) {
		t.Errorf("the spec declares no GET %s; it declares %v", bundleAPI, ops)
	}
}

// Criterion: the bundle is a tar.gz of the resolved set laid out for the
// Claude target: the Project's bundle and agent win whole, the draft agent is
// left out, and every path is clean and relative.
func TestBundleIsATarGzOfTheResolvedSetLaidOutForClaude(t *testing.T) {
	srv := pullServer(t, absContent(t))
	resp, body := bundleGet(t, srv, pullProject, "claude", nil)
	wantBundleOK(t, resp, body)
	if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" && ct != "application/x-gzip" {
		t.Errorf("Content-Type %q, want application/gzip", ct)
	}
	wantSet(t, "shop bundle", bundleContents(readBundle(t, body)), shopSet(), shopAgents)
}

// Criterion: without a Project, and for an unregistered one, the bundle is
// the global set.
func TestBundleWithoutAProjectIsTheGlobalSet(t *testing.T) {
	srv := pullServer(t, absContent(t))
	for _, project := range []string{"", "github.com/acme/unknown"} {
		resp, body := bundleGet(t, srv, project, "claude", nil)
		wantBundleOK(t, resp, body)
		wantSet(t, "global bundle for "+project, bundleContents(readBundle(t, body)), globalSet(), globalAgents)
	}
}

// Criterion: the ETag is the set digest; an unchanged set is a 304 without a
// body; a different set has a different digest.
func TestBundleETagIsTheSetDigestAndAnUnchangedSetIs304(t *testing.T) {
	srv := pullServer(t, absContent(t))
	resp, body := bundleGet(t, srv, pullProject, "claude", nil)
	wantBundleOK(t, resp, body)
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("the bundle carries no ETag")
	}
	resp2, body2 := bundleGet(t, srv, pullProject, "claude", nil)
	if resp2.Header.Get("ETag") != etag || !bytes.Equal(body, body2) {
		t.Errorf("the same set gave ETag %q then %q, and the bodies equal is %v; want a stable digest and archive",
			etag, resp2.Header.Get("ETag"), bytes.Equal(body, body2))
	}

	resp, body = bundleGet(t, srv, pullProject, "claude", func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if resp.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Errorf("If-None-Match %s: status %d with %d body bytes, want 304 and none", etag, resp.StatusCode, len(body))
	}
	if resp.Header.Get("ETag") != etag {
		t.Errorf("the 304 carries ETag %q, want %q", resp.Header.Get("ETag"), etag)
	}
	resp, body = bundleGet(t, srv, pullProject, "claude", func(r *http.Request) { r.Header.Set("If-None-Match", `"some-other-digest"`) })
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Errorf("a stale If-None-Match: status %d, want 200 with the archive", resp.StatusCode)
	}
	if gl, _ := bundleGet(t, srv, "", "claude", nil); gl.Header.Get("ETag") == etag {
		t.Errorf("the global set and the shop set share the digest %q", etag)
	}
}

// Criterion: a changed set has a new digest, and the old digest no longer
// gets a 304.
func TestBundleETagChangesWhenTheSetChanges(t *testing.T) {
	content := copyContent(t)
	srv := pullServer(t, content)
	resp, _ := bundleGet(t, srv, pullProject, "claude", nil)
	old := resp.Header.Get("ETag")
	if old == "" {
		t.Fatal("the bundle carries no ETag")
	}
	edit := filepath.Join(content, "global", "skills", "lint", "SKILL.md")
	if err := os.WriteFile(edit, []byte("# Lint, changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := pullServer(t, content)
	resp, body := bundleGet(t, changed, pullProject, "claude", nil)
	wantBundleOK(t, resp, body)
	if resp.Header.Get("ETag") == old {
		t.Errorf("an edited skill kept the digest %q", old)
	}
	if got := bundleContents(readBundle(t, body))[pLintSkill]; got != "# Lint, changed\n" {
		t.Errorf("the edited skill is %q in the bundle", got)
	}
	resp, _ = bundleGet(t, changed, pullProject, "claude", func(r *http.Request) { r.Header.Set("If-None-Match", old) })
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the old digest against the new set: status %d, want 200", resp.StatusCode)
	}
}

// Criterion: a script's executable bit travels in the archive.
func TestBundleKeepsTheExecutableBit(t *testing.T) {
	content := copyContent(t)
	script := filepath.Join(content, "global", "skills", "deploy", "scripts", "run.sh")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	resp, body := bundleGet(t, pullServer(t, content), "", "claude", nil)
	wantBundleOK(t, resp, body)
	files := readBundle(t, body)
	if m := files[pDeployScript].mode; m&0o111 == 0 {
		t.Errorf("the executable script has mode %o in the bundle", m)
	}
	if m := files[pDeploySkill].mode; m&0o111 != 0 {
		t.Errorf("SKILL.md has mode %o in the bundle, want no executable bit", m)
	}
}

// Criterion (negative): an unknown target, an invalid project and a missing
// token are refused with the envelope, and nothing of the request is echoed.
func TestBundleRefusesAnUnknownTargetABadProjectAndNoToken(t *testing.T) {
	srv := pullServer(t, absContent(t))
	for _, target := range []string{"nope", "../../etc", "CLAUDE", "claude%0A"} {
		resp, body := do(t, http.MethodGet, srv.URL+bundleAPI+"?target="+target, bearer(readerToken))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("target=%s: status %d, want 400: %s", target, resp.StatusCode, body)
			continue
		}
		if code, _ := errorHint(t, body); code != "bad_request" {
			t.Errorf("target=%s: code %q, want bad_request", target, code)
		}
		if strings.Contains(string(body), "etc") {
			t.Errorf("target=%s: the answer echoes the request: %s", target, body)
		}
	}
	for _, bad := range []string{"not-a-project", "../../etc", "github.com/acme/shop%0A", "a/b/c/d"} {
		resp, body := do(t, http.MethodGet, srv.URL+bundleAPI+"?target=claude&project="+bad, bearer(readerToken))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("project=%s: status %d, want 400: %s", bad, resp.StatusCode, body)
		}
	}
	resp, body := do(t, http.MethodGet, srv.URL+bundleAPI+"?target=claude", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401: %s", resp.StatusCode, body)
	}
	resp, body = do(t, http.MethodGet, srv.URL+bundleAPI+"?target=claude", bearer("wrong-"+readerToken))
	if resp.StatusCode != http.StatusUnauthorized || strings.Contains(string(body), readerToken) {
		t.Errorf("a wrong token: status %d, want 401 without echoing it: %s", resp.StatusCode, body)
	}
}

// --- the CLI boundary ---

// pullCLI runs goldie pull in dir (the working directory for the call)
// against the server at addr, with the reader token.
func pullCLI(t *testing.T, addr, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	var out bytes.Buffer
	env := cli.Env{
		Lookup:   func(string) (string, bool) { return "", false },
		ReadFile: os.ReadFile,
		Out:      &out,
		Err:      io.Discard,
	}
	args = append([]string{"pull"}, args...)
	fset := flag.NewFlagSet("pull", flag.ContinueOnError)
	exec := cli.Command("pull", fset)
	pf := pflag.NewFlagSet("pull", pflag.ContinueOnError)
	pf.SetOutput(io.Discard)
	pf.AddGoFlagSet(fset)
	if err := pf.Parse(append(args[1:], "--addr", addr, "--token", readerToken)); err != nil {
		return out.String(), err
	}
	err := exec(context.Background(), pf.Args(), env)
	return out.String(), err
}

// pullShop pulls the shop Project into dir and fails the test on an error.
func pullShop(t *testing.T, addr, dir string, extra ...string) string {
	t.Helper()
	out, err := pullCLI(t, addr, dir, append([]string{"--project", pullProject}, extra...)...)
	if err != nil {
		t.Fatalf("goldie pull: %v\n%s", err, out)
	}
	return out
}

// snapshot is every entry under dir by slash path: kind, mode and content (or
// link target). Directories are listed so a created one shows.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			l, _ := os.Readlink(p)
			out[rel] = "link:" + l
		case info.IsDir():
			out[rel] = "dir"
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = "file:" + info.Mode().Perm().String() + ":" + string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func inNamespace(p string) bool {
	return strings.HasPrefix(p, ".claude/skills/goldie-") || strings.HasPrefix(p, ".claude/agents/goldie-")
}

// isStamp allows the digest stamp: under .goldie/, or a first element under
// .claude/ that starts with "goldie". Parent directories of namespace paths
// are allowed too.
func isStamp(p string) bool {
	if p == ".goldie" || strings.HasPrefix(p, ".goldie/") {
		return true
	}
	rest, ok := strings.CutPrefix(p, ".claude/")
	return ok && strings.HasPrefix(rest, "goldie")
}

// wantOutsideUntouched fails for every path of before that is outside the
// namespace and is changed or gone in after, and for every new path in after
// that is neither in the namespace nor the stamp.
func wantOutsideUntouched(t *testing.T, before, after map[string]string) {
	t.Helper()
	for p, v := range before {
		if inNamespace(p) {
			continue
		}
		if got, ok := after[p]; !ok || got != v {
			t.Errorf("%s outside the Goldie namespace was changed or removed by the pull (was %q, now %q)", p, v, got)
		}
	}
	for p := range after {
		if _, ok := before[p]; ok || inNamespace(p) || isStamp(p) {
			continue
		}
		switch p {
		case ".claude", ".claude/skills", ".claude/agents":
			continue
		}
		t.Errorf("the pull created %s outside the Goldie namespace", p)
	}
}

// namespaceFiles are the regular files the snapshot holds in the namespace.
func namespaceFiles(s map[string]string) fileSet {
	out := fileSet{}
	for p, v := range s {
		if v, ok := strings.CutPrefix(v, "file:"); ok && inNamespace(p) {
			_, content, _ := strings.Cut(v, ":")
			out[p] = content
		}
	}
	return out
}

// userFiles are the project's own files, in and beside .claude, that no pull
// may touch.
func userFiles() map[string]string {
	return map[string]string{
		"CLAUDE.md":                    "# my rules\n",
		"src/main.go":                  "package main\n",
		".claude/settings.json":        "{\"mine\":true}\n",
		".claude/skills/mine/SKILL.md": "# mine\n",
		".claude/skills/goldieish/x":   "not goldie-\n",
		".claude/agents/mine.md":       "my agent\n",
		".claude/agents/goldie.md":     "no dash\n",
		".claude/commands/deploy.md":   "my command\n",
	}
}

// Criterion: the Claude target writes .claude/skills/goldie-<name>/ and
// .claude/agents/goldie-<name>.md, from the repository root even when run in a
// subdirectory, and the default target is claude.
func TestPullWritesTheClaudeNamespace(t *testing.T) {
	srv := pullServer(t, absContent(t))
	repo := gitCheckout(t, map[string]string{"sub/dir/keep": "x"})
	before := snapshot(t, repo)
	pullShop(t, srv.URL, filepath.Join(repo, "sub", "dir"))
	after := snapshot(t, repo)
	wantSet(t, "pulled", namespaceFiles(after), shopSet(), shopAgents)
	wantOutsideUntouched(t, before, after)

	// An explicit --target claude is the same, and global without a Project.
	repo2 := gitCheckout(t, nil)
	if out, err := pullCLI(t, srv.URL, repo2, "--target", "claude"); err != nil {
		t.Fatalf("goldie pull --target claude: %v\n%s", err, out)
	}
	wantSet(t, "pulled global", namespaceFiles(snapshot(t, repo2)), globalSet(), globalAgents)
}

// Criterion: the executable bit of a script is kept on disk.
func TestPullKeepsTheExecutableBit(t *testing.T) {
	content := copyContent(t)
	if err := os.Chmod(filepath.Join(content, "global", "skills", "deploy", "scripts", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := gitCheckout(t, nil)
	if out, err := pullCLI(t, pullServer(t, content).URL, repo); err != nil {
		t.Fatalf("goldie pull: %v\n%s", err, out)
	}
	fi, err := os.Stat(filepath.Join(repo, filepath.FromSlash(pDeployScript)))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("the pulled script has mode %v, want the owner's executable bit", fi.Mode().Perm())
	}
	fi, err = os.Stat(filepath.Join(repo, filepath.FromSlash(pDeploySkill)))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 != 0 {
		t.Errorf("SKILL.md has mode %v, want no executable bit", fi.Mode().Perm())
	}
}

// Criterion: the namespace is replaced wholesale (a stale file in a goldie
// skill, a goldie skill and agent nobody serves any more, and an Item removed
// upstream disappear) and nothing outside it is touched.
func TestPullReplacesTheNamespaceWholesaleAndTouchesNothingElse(t *testing.T) {
	files := userFiles()
	files[".claude/skills/goldie-old/SKILL.md"] = "# removed long ago\n"
	files[".claude/skills/goldie-deploy/stale.txt"] = "stale\n"
	files[".claude/skills/goldie-deploy/SKILL.md"] = "# hand-edited\n"
	files[".claude/agents/goldie-gone.md"] = "gone\n"
	repo := gitCheckout(t, files)
	before := snapshot(t, repo)

	content := copyContent(t)
	pullShop(t, pullServer(t, content).URL, repo)
	after := snapshot(t, repo)
	wantSet(t, "after the first pull", namespaceFiles(after), shopSet(), shopAgents)
	for _, gone := range []string{".claude/skills/goldie-old/SKILL.md", ".claude/skills/goldie-deploy/stale.txt", ".claude/agents/goldie-gone.md"} {
		if _, ok := after[gone]; ok {
			t.Errorf("%s survived the pull", gone)
		}
	}
	wantOutsideUntouched(t, before, after)

	// An Item removed upstream disappears on the next pull.
	if err := os.RemoveAll(filepath.Join(content, "global", "skills", "lint")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(content, "global", "agents", "planner.md")); err != nil {
		t.Fatal(err)
	}
	pullShop(t, pullServer(t, content).URL, repo)
	again := snapshot(t, repo)
	wantSet(t, "after upstream removals", namespaceFiles(again), fileSet{pDeploySkill: "# Deploy shop\n", pDeployList: "check it\n"},
		map[string][]string{pReviewerAgent: {"You are the shop reviewer."}})
	wantOutsideUntouched(t, before, again)
}

// Criterion: --dry-run lists the files it would write and writes nothing; once
// the set is pulled, it lists none.
func TestPullDryRunListsTheFilesAndWritesNothing(t *testing.T) {
	srv := pullServer(t, absContent(t))
	repo := gitCheckout(t, userFiles())
	before := snapshot(t, repo)
	out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--dry-run")
	if err != nil {
		t.Fatalf("goldie pull --dry-run: %v\n%s", err, out)
	}
	for p := range wantAll(shopSet(), shopAgents) {
		if !strings.Contains(out, p) {
			t.Errorf("--dry-run does not list %s:\n%s", p, out)
		}
	}
	if after := snapshot(t, repo); !mapsEqual(before, after) {
		t.Errorf("--dry-run changed the project: before %v, after %v", before, after)
	}

	pullShop(t, srv.URL, repo)
	out, err = pullCLI(t, srv.URL, repo, "--project", pullProject, "--dry-run")
	if err != nil {
		t.Fatalf("goldie pull --dry-run after a pull: %v\n%s", err, out)
	}
	for _, p := range []string{pDeploySkill, pLintSkill, pReviewerAgent} {
		if strings.Contains(out, p) {
			t.Errorf("--dry-run of an unchanged set still lists %s:\n%s", p, out)
		}
	}
}

// Criterion (#641): a dry run predicts a real pull, so a symlinked .claude is
// refused with the real pull's error, lists nothing and writes nothing.
func TestPullDryRunRefusesASymlinkedClaudeLikeARealPull(t *testing.T) {
	srv := pullServer(t, absContent(t))
	outside := t.TempDir()
	repo := gitCheckout(t, nil)
	if err := os.Symlink(outside, filepath.Join(repo, ".claude")); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, repo)

	_, realErr := pullCLI(t, srv.URL, repo, "--project", pullProject)
	if realErr == nil {
		t.Fatal("a real pull through a symlinked .claude succeeded")
	}
	out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--dry-run")
	if err == nil {
		t.Fatalf("--dry-run with a symlinked .claude succeeded:\n%s", out)
	}
	if err.Error() != realErr.Error() {
		t.Errorf("--dry-run error %q differs from the real pull's %q", err, realErr)
	}
	for _, w := range []string{"would write", "would remove"} {
		if strings.Contains(out, w) {
			t.Errorf("--dry-run printed %q for a refused pull:\n%s", w, out)
		}
	}
	if after := snapshot(t, repo); !mapsEqual(before, after) {
		t.Errorf("the project changed: before %v, after %v", before, after)
	}
	if left := snapshot(t, outside); len(left) != 0 {
		t.Errorf("the link target gained %v", left)
	}
}

// Criterion (#641): with a real or an absent .claude the dry run still lists
// every file it would write, and writes nothing.
func TestPullDryRunWithARealOrAbsentClaudeIsUnchanged(t *testing.T) {
	srv := pullServer(t, absContent(t))
	for name, files := range map[string]map[string]string{
		"absent": nil,
		"real":   {".claude/settings.local.json": "{}\n"},
	} {
		repo := gitCheckout(t, files)
		before := snapshot(t, repo)
		out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--dry-run")
		if err != nil {
			t.Fatalf("%s .claude: goldie pull --dry-run: %v\n%s", name, err, out)
		}
		for p := range wantAll(shopSet(), shopAgents) {
			if !strings.Contains(out, "would write "+p) {
				t.Errorf("%s .claude: --dry-run does not list %q:\n%s", name, "would write "+p, out)
			}
		}
		if after := snapshot(t, repo); !mapsEqual(before, after) {
			t.Errorf("%s .claude: --dry-run changed the project: before %v, after %v", name, before, after)
		}
	}
}

func wantAll(skills fileSet, agents map[string][]string) map[string]bool {
	out := map[string]bool{}
	for p := range skills {
		out[p] = true
	}
	for p := range agents {
		out[p] = true
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// Criterion: an unchanged set is a no-op: nothing is rewritten.
func TestPullOfAnUnchangedSetRewritesNothing(t *testing.T) {
	srv := pullServer(t, absContent(t))
	repo := gitCheckout(t, userFiles())
	pullShop(t, srv.URL, repo)
	first := snapshot(t, repo)
	wantSet(t, "first pull", namespaceFiles(first), shopSet(), shopAgents)
	times := map[string]time.Time{}
	for p := range first {
		if fi, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(p))); err == nil {
			times[p] = fi.ModTime()
		}
	}
	time.Sleep(20 * time.Millisecond)
	pullShop(t, srv.URL, repo)
	if second := snapshot(t, repo); !mapsEqual(first, second) {
		t.Errorf("a second pull of the same set changed the project: %v then %v", first, second)
	}
	for p, was := range times {
		if fi, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(p))); err != nil || !fi.ModTime().Equal(was) {
			t.Errorf("%s was rewritten by a pull of an unchanged set", p)
		}
	}
}

// Criterion: --check exits non-zero while the stamp is stale (never pulled,
// or the set changed upstream), zero once pulled, and writes nothing.
func TestPullCheckFailsWhileTheStampIsStale(t *testing.T) {
	content := copyContent(t)
	srv := pullServer(t, content)
	repo := gitCheckout(t, userFiles())
	before := snapshot(t, repo)

	out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--check")
	if err == nil {
		t.Fatalf("--check before any pull succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error()+out, "goldie pull") {
		t.Errorf("--check does not say to run goldie pull: %v %q", err, out)
	}
	if after := snapshot(t, repo); !mapsEqual(before, after) {
		t.Errorf("--check wrote to the project: %v then %v", before, after)
	}

	pullShop(t, srv.URL, repo)
	pulled := snapshot(t, repo)
	if out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--check"); err != nil {
		t.Errorf("--check right after a pull: %v\n%s", err, out)
	}
	// Another server over the same set agrees: the stamp is the set digest.
	if out, err := pullCLI(t, pullServer(t, content).URL, repo, "--project", pullProject, "--check"); err != nil {
		t.Errorf("--check against a second server of the same set: %v\n%s", err, out)
	}

	if err := os.WriteFile(filepath.Join(content, "global", "skills", "lint", "SKILL.md"), []byte("# Lint v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := pullServer(t, content)
	if out, err := pullCLI(t, changed.URL, repo, "--project", pullProject, "--check"); err == nil {
		t.Errorf("--check after the set changed upstream succeeded:\n%s", out)
	}
	if after := snapshot(t, repo); !mapsEqual(pulled, after) {
		t.Errorf("a failing --check wrote to the project: %v then %v", pulled, after)
	}
	pullShop(t, changed.URL, repo)
	if out, err := pullCLI(t, changed.URL, repo, "--project", pullProject, "--check"); err != nil {
		t.Errorf("--check after pulling the new set: %v\n%s", err, out)
	}
}

// Criterion (negative): misuse writes nothing: outside a repository, an
// unknown target, both --dry-run and --check, and stray words.
func TestPullRefusesMisuseAndWritesNothing(t *testing.T) {
	srv := pullServer(t, absContent(t))
	repo := gitCheckout(t, userFiles())
	before := snapshot(t, repo)
	for name, args := range map[string][]string{
		"unknown target":      {"--target", "nope"},
		"path-like target":    {"--target", "../../etc"},
		"dry-run and check":   {"--dry-run", "--check"},
		"stray word":          {"everything"},
		"bad project spelled": {"--project", "../../etc"},
	} {
		if out, err := pullCLI(t, srv.URL, repo, args...); err == nil {
			t.Errorf("%s: goldie pull succeeded:\n%s", name, out)
		}
	}
	if after := snapshot(t, repo); !mapsEqual(before, after) {
		t.Errorf("a refused pull changed the project: %v then %v", before, after)
	}

	loose := outsideRepository(t)
	if out, err := pullCLI(t, srv.URL, loose); err == nil {
		t.Errorf("goldie pull outside a repository succeeded:\n%s", out)
	}
	if left := snapshot(t, loose); len(left) != 0 {
		t.Errorf("a pull outside a repository wrote %v", left)
	}
}

// Criterion (negative): a server that refuses, or a wrong token, leaves the
// project and its existing namespace as they were.
func TestPullFailureLeavesTheProjectAsItWas(t *testing.T) {
	files := userFiles()
	files[".claude/skills/goldie-old/SKILL.md"] = "# kept on failure\n"
	repo := gitCheckout(t, files)
	before := snapshot(t, repo)

	srv := pullServer(t, absContent(t))
	t.Chdir(repo)
	var out bytes.Buffer
	env := cli.Env{Lookup: func(string) (string, bool) { return "", false }, ReadFile: os.ReadFile, Out: &out, Err: io.Discard}
	fset := flag.NewFlagSet("pull", flag.ContinueOnError)
	exec := cli.Command("pull", fset)
	pf := pflag.NewFlagSet("pull", pflag.ContinueOnError)
	pf.AddGoFlagSet(fset)
	if err := pf.Parse([]string{"--addr", srv.URL, "--token", "wrong-" + readerToken}); err != nil {
		t.Fatal(err)
	}
	err := exec(context.Background(), pf.Args(), env)
	if err == nil {
		t.Error("a wrong token pulled")
	} else if strings.Contains(err.Error(), readerToken) {
		t.Errorf("the error carries the token: %v", err)
	}
	for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound} {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"x","message":"no","details":{}}}`))
		}))
		t.Cleanup(stub.Close)
		if out, err := pullCLI(t, stub.URL, repo); err == nil {
			t.Errorf("a %d answer pulled:\n%s", status, out)
		}
	}
	if after := snapshot(t, repo); !mapsEqual(before, after) {
		t.Errorf("a failed pull changed the project: %v then %v", before, after)
	}
}

// Criterion (security): the namespace is never written through a symlink, so
// a linked .claude/skills or .claude/agents cannot send files elsewhere.
func TestPullDoesNotWriteThroughASymlinkedNamespace(t *testing.T) {
	srv := pullServer(t, absContent(t))
	for _, link := range []string{".claude/skills", ".claude/agents", ".claude/skills/goldie-deploy", ".claude"} {
		outside := t.TempDir()
		repo := gitCheckout(t, nil)
		if err := os.MkdirAll(filepath.Join(repo, filepath.FromSlash(filepath.Dir(link))), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(repo, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
		// Either outcome is acceptable (a refusal, or a pull that routes around
		// the link); writing into the linked directory is not.
		_, _ = pullCLI(t, srv.URL, repo, "--project", pullProject)
		if left := snapshot(t, outside); len(left) != 0 {
			t.Errorf("%s linked outside: the pull wrote %v there", link, left)
		}
	}
}

// --- hostile archives ---

// hostileCases build the archives from the directory the entries must not
// reach. Each starts with a valid entry, so a pull that wrote as it went would
// show it.
func hostileCases(outside string) map[string][]tarEntry {
	ok := tarEntry{name: ".claude/skills/goldie-ok/SKILL.md", body: "# ok\n"}
	return map[string][]tarEntry{
		"absolute path":                  {ok, {name: filepath.Join(outside, "pwned"), body: "x"}},
		"absolute path in the namespace": {ok, {name: "/.claude/skills/goldie-x/SKILL.md", body: "x"}},
		"parent directory":               {ok, {name: "../goldie-escape", body: "x"}},
		"parent directory deep":          {ok, {name: ".claude/skills/goldie-x/../../../escape", body: "x"}},
		"dot-dot back into the project":  {ok, {name: ".claude/skills/goldie-x/../../settings.json", body: "{}"}},
		"symlink to outside":             {ok, {name: ".claude/skills/goldie-x/link", typ: tar.TypeSymlink, link: outside}},
		"symlink relative":               {ok, {name: ".claude/skills/goldie-x/link", typ: tar.TypeSymlink, link: "../../../../outside"}},
		"file through a symlink": {ok,
			{name: ".claude/skills/goldie-x/link", typ: tar.TypeSymlink, link: outside},
			{name: ".claude/skills/goldie-x/link/pwned", body: "x"}},
		"symlink replacing a file": {ok, {name: ".claude/agents/goldie-evil.md", typ: tar.TypeSymlink, link: "../../CLAUDE.md"}},
		"hard link":                {ok, {name: ".claude/skills/goldie-x/hard", typ: tar.TypeLink, link: "CLAUDE.md"}},
		"hard link to outside":     {ok, {name: ".claude/skills/goldie-x/hard", typ: tar.TypeLink, link: "../../../CLAUDE.md"}},
		"device":                   {ok, {name: ".claude/skills/goldie-x/dev", typ: tar.TypeChar}},
		"fifo":                     {ok, {name: ".claude/skills/goldie-x/fifo", typ: tar.TypeFifo}},
		"project file":             {ok, {name: "CLAUDE.md", body: "owned\n"}},
		"settings":                 {ok, {name: ".claude/settings.json", body: "{}"}},
		"foreign skill":            {ok, {name: ".claude/skills/mine/SKILL.md", body: "owned\n"}},
		"foreign agent":            {ok, {name: ".claude/agents/mine.md", body: "owned\n"}},
		"prefix without a dash":    {ok, {name: ".claude/skills/goldieish/x", body: "owned\n"}},
		"the skills directory":     {ok, {name: ".claude/skills/SKILL.md", body: "owned\n"}},
	}
}

// Criterion: extraction rejects absolute paths, .., symlinks and everything
// else outside the namespace, and a refused archive writes nothing: not its
// valid entries, and it does not wipe the existing namespace either.
func TestPullRejectsHostileArchivesAndWritesNothing(t *testing.T) {
	// The control: the same stub with a safe archive is accepted, so the
	// refusals below are for the hostile entries.
	archives := map[string][]byte{}
	outsideProbe := t.TempDir()
	for name, entries := range hostileCases(outsideProbe) {
		archives[name] = buildTarGz(t, entries)
	}
	var current []byte
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("ETag", `"hostile-stub"`)
		_, _ = w.Write(current)
	}))
	t.Cleanup(stub.Close)

	files := userFiles()
	files[".claude/skills/goldie-old/SKILL.md"] = "# existing\n"

	current = buildTarGz(t, []tarEntry{{name: ".claude/skills/goldie-ok/SKILL.md", body: "# ok\n"}})
	control := gitCheckout(t, files)
	if out, err := pullCLI(t, stub.URL, control); err != nil {
		t.Fatalf("the safe control archive was refused: %v\n%s", err, out)
	}
	if got := namespaceFiles(snapshot(t, control)); !mapsEqual(got, fileSet{".claude/skills/goldie-ok/SKILL.md": "# ok\n"}) {
		t.Fatalf("the control pull left %v", got)
	}

	names := make([]string, 0, len(archives))
	for n := range archives {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			current = archives[name]
			repo := gitCheckout(t, files)
			before := snapshot(t, repo)
			outside := t.TempDir() // a fresh directory the entries could aim at
			outsideBefore := snapshot(t, outside)
			out, err := pullCLI(t, stub.URL, repo)
			if err == nil {
				t.Errorf("the hostile archive was accepted:\n%s", out)
			}
			if after := snapshot(t, repo); !mapsEqual(before, after) {
				t.Errorf("a refused archive changed the project:\nbefore %v\nafter  %v", before, after)
			}
			if after := snapshot(t, outside); !mapsEqual(outsideBefore, after) {
				t.Errorf("a refused archive wrote outside the project: %v", after)
			}
			if left := snapshot(t, outsideProbe); len(left) != 0 {
				t.Errorf("a refused archive wrote into %s: %v", outsideProbe, left)
			}
		})
	}

	// An archive that is not a gzip tar at all is refused the same way.
	for name, junk := range map[string][]byte{
		"not gzip":       []byte("<html>not an archive</html>"),
		"empty body":     nil,
		"truncated gzip": archives["parent directory"][:len(archives["parent directory"])/2],
	} {
		t.Run(name, func(t *testing.T) {
			current = junk
			repo := gitCheckout(t, files)
			before := snapshot(t, repo)
			if out, err := pullCLI(t, stub.URL, repo); err == nil {
				t.Errorf("a body that is not a bundle was accepted:\n%s", out)
			}
			if after := snapshot(t, repo); !mapsEqual(before, after) {
				t.Errorf("a refused body changed the project: %v then %v", before, after)
			}
		})
	}
}

// --- a damaged namespace (#696) ---

// bundleCall is one GET /api/v1/bundle the server answered.
type bundleCall struct {
	status      int
	conditional bool
	bytes       int
}

// bundleLog records the bundle responses of a countingPullServer.
type bundleLog struct {
	mu    sync.Mutex
	calls []bundleCall
}

func (b *bundleLog) all() []bundleCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.calls)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	n      int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.n += len(b)
	return r.ResponseWriter.Write(b)
}

// countingPullServer is pullServer with a record of the bundle responses.
func countingPullServer(t *testing.T, content string) (*httptest.Server, *bundleLog) {
	t.Helper()
	h := activityHandler(t, platform.Config{ContentDir: content, RateLimit: roomyRateLimit})
	rec := &bundleLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sr := &statusRecorder{ResponseWriter: w}
		h.ServeHTTP(sr, r)
		if r.URL.Path == bundleAPI {
			rec.mu.Lock()
			rec.calls = append(rec.calls, bundleCall{sr.status, r.Header.Get("If-None-Match") != "", sr.n})
			rec.mu.Unlock()
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// Criterion: after a pulled file is deleted or edited by hand (a skill file or
// an agent file), goldie pull --check exits non-zero and names goldie pull,
// writing nothing; goldie pull then restores the namespace exactly, and
// --check is green again.
func TestPullCheckAndPullCatchADamagedNamespace(t *testing.T) {
	damage := map[string]func(t *testing.T, path string){
		"a deleted file": func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
		"an edited file": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("edited by hand\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	targets := map[string]string{"a skill file": pLintSkill, "an agent file": pPlannerAgent}
	for dname, do := range damage {
		for tname, rel := range targets {
			t.Run(dname+" in "+tname, func(t *testing.T) {
				srv, _ := countingPullServer(t, absContent(t))
				repo := gitCheckout(t, userFiles())
				pullShop(t, srv.URL, repo)
				pulled := snapshot(t, repo)

				do(t, filepath.Join(repo, filepath.FromSlash(rel)))
				damaged := snapshot(t, repo)

				out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--check")
				if err == nil {
					t.Errorf("--check succeeded on a damaged namespace:\n%s", out)
				} else if !strings.Contains(err.Error()+out, "goldie pull") {
					t.Errorf("--check does not say to run goldie pull: %v %q", err, out)
				}
				if after := snapshot(t, repo); !mapsEqual(damaged, after) {
					t.Errorf("a failing --check wrote to the project: %v then %v", damaged, after)
				}

				pullShop(t, srv.URL, repo)
				if after := snapshot(t, repo); !mapsEqual(pulled, after) {
					t.Errorf("goldie pull did not restore the namespace: want %v, got %v", pulled, after)
				}
				if out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--check"); err != nil {
					t.Errorf("--check after the restoring pull: %v\n%s", err, out)
				}
			})
		}
	}
}

// Criterion: an untouched namespace with a current server set is still a
// no-op, and the server returns no archive: the conditional request is answered
// 304 with an empty body, for goldie pull and for --check.
func TestPullOfAnUntouchedNamespaceStillGets304(t *testing.T) {
	srv, rec := countingPullServer(t, absContent(t))
	repo := gitCheckout(t, userFiles())
	pullShop(t, srv.URL, repo)
	first := snapshot(t, repo)
	if calls := rec.all(); len(calls) != 1 || calls[0].status != http.StatusOK || calls[0].bytes == 0 {
		t.Fatalf("the first pull did not fetch the archive once: %+v", calls)
	}

	pullShop(t, srv.URL, repo)
	if out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--check"); err != nil {
		t.Errorf("--check on an untouched namespace: %v\n%s", err, out)
	}
	calls := rec.all()
	if len(calls) != 3 {
		t.Fatalf("want one request per run, got %+v", calls)
	}
	for i, c := range calls[1:] {
		if !c.conditional || c.status != http.StatusNotModified || c.bytes != 0 {
			t.Errorf("run %d on an untouched namespace: %+v, want a conditional request answered 304 with no body", i+2, c)
		}
	}
	if after := snapshot(t, repo); !mapsEqual(first, after) {
		t.Errorf("a no-op pull changed the project: %v then %v", first, after)
	}
}

// --- goldie pull --quiet and --if-pulled (#669) ---

const stampPath = ".claude/goldie-pull.digest"

// bundleCalls is how many bundle requests the server has answered.
func bundleCalls(rec *bundleLog) int { return len(rec.all()) }

// Criterion 1: --quiet prints nothing on stdout when a pull writes files and
// when the namespace is already up to date.
func TestPullQuietPrintsNothingWhenPullingOrUpToDate(t *testing.T) {
	content := copyContent(t)
	srv := pullServer(t, content)
	repo := gitCheckout(t, userFiles())

	out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--quiet")
	if err != nil {
		t.Fatalf("goldie pull --quiet: %v\n%s", err, out)
	}
	if out != "" {
		t.Errorf("--quiet printed on a pull that wrote files: %q", out)
	}
	wantSet(t, "pulled", namespaceFiles(snapshot(t, repo)), shopSet(), shopAgents)

	if out, err = pullCLI(t, srv.URL, repo, "--project", pullProject, "--quiet"); err != nil || out != "" {
		t.Errorf("--quiet on an up-to-date namespace: err %v, stdout %q", err, out)
	}

	if err := os.WriteFile(filepath.Join(content, "global", "skills", "lint", "SKILL.md"), []byte("# Lint v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err = pullCLI(t, pullServer(t, content).URL, repo, "--project", pullProject, "--quiet"); err != nil || out != "" {
		t.Errorf("--quiet on a changed set: err %v, stdout %q", err, out)
	}
	if got := readPulled(t, repo, pLintSkill); got != "# Lint v2\n" {
		t.Errorf("--quiet did not write the changed set: %q", got)
	}
}

// Criterion 1: errors keep their exit path under --quiet: a failing server is
// still an error (not misuse) and nothing is written or printed on stdout.
func TestPullQuietStillFailsWhenTheServerFails(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	repo := gitCheckout(t, userFiles())
	before := snapshot(t, repo)
	out, err := pullCLI(t, broken.URL, repo, "--project", pullProject, "--quiet")
	if err == nil {
		t.Fatalf("--quiet against a failing server succeeded:\n%s", out)
	}
	var ue *cli.UsageError
	if errors.As(err, &ue) {
		t.Errorf("a server failure is not misuse (exit 2): %v", err)
	}
	if out != "" {
		t.Errorf("--quiet printed on stdout for an error: %q", out)
	}
	if after := snapshot(t, repo); !mapsEqual(before, after) {
		t.Errorf("a failed --quiet pull changed the project: %v then %v", before, after)
	}
}

// Criterion 2: --if-pulled in a repository without a stamp sends no request,
// writes nothing and succeeds, also when the stamp is one readStamp rejects
// (a symlink, oversized, malformed).
func TestPullIfPulledWithoutAStampDoesNothing(t *testing.T) {
	srv, rec := countingPullServer(t, absContent(t))
	outside := filepath.Join(t.TempDir(), "valid-stamp")
	if err := os.WriteFile(outside, []byte("sha256-abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(t *testing.T, repo string){
		"no stamp": func(*testing.T, string) {},
		"symlinked stamp": func(t *testing.T, repo string) {
			if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(repo, stampPath)); err != nil {
				t.Fatal(err)
			}
		},
		"oversized stamp": func(t *testing.T, repo string) {
			writeCheckoutFile(t, repo, stampPath, "sha256-abc123\n"+strings.Repeat("x", 300))
		},
		"malformed stamp": func(t *testing.T, repo string) {
			writeCheckoutFile(t, repo, stampPath, "not a digest!\n")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			repo := gitCheckout(t, userFiles())
			setup(t, repo)
			before := snapshot(t, repo)
			calls := bundleCalls(rec)
			for _, extra := range [][]string{{"--if-pulled"}, {"--if-pulled", "--quiet"}, {"--if-pulled", "--check"}} {
				out, err := pullCLI(t, srv.URL, repo, append([]string{"--project", pullProject}, extra...)...)
				if err != nil {
					t.Errorf("%v without a usable stamp: %v\n%s", extra, err, out)
				}
				if slices.Contains(extra, "--quiet") && out != "" {
					t.Errorf("%v printed %q", extra, out)
				}
			}
			if got := bundleCalls(rec); got != calls {
				t.Errorf("--if-pulled without a stamp sent %d bundle requests", got-calls)
			}
			if after := snapshot(t, repo); !mapsEqual(before, after) {
				t.Errorf("--if-pulled without a stamp wrote to the project: %v then %v", before, after)
			}
		})
	}
}

// Criterion 2: with a stamp, --if-pulled pulls as today: an up-to-date
// namespace costs a conditional request answered 304, a changed set is
// written, and with --check a stale namespace fails.
func TestPullIfPulledWithAStampPullsAsToday(t *testing.T) {
	content := copyContent(t)
	srv, rec := countingPullServer(t, content)
	repo := gitCheckout(t, userFiles())
	pullShop(t, srv.URL, repo)
	first := snapshot(t, repo)
	calls := bundleCalls(rec)

	out, err := pullCLI(t, srv.URL, repo, "--project", pullProject, "--if-pulled")
	if err != nil {
		t.Fatalf("--if-pulled on an up-to-date namespace: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already up to date") {
		t.Errorf("--if-pulled on an up-to-date namespace says %q", out)
	}
	all := rec.all()
	if len(all) != calls+1 || !all[calls].conditional || all[calls].status != http.StatusNotModified {
		t.Errorf("--if-pulled with a stamp: %+v, want one conditional request answered 304", all[calls:])
	}
	if after := snapshot(t, repo); !mapsEqual(first, after) {
		t.Errorf("an up-to-date --if-pulled changed the project")
	}

	if err := os.WriteFile(filepath.Join(content, "global", "skills", "lint", "SKILL.md"), []byte("# Lint v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := pullServer(t, content)
	if out, err := pullCLI(t, changed.URL, repo, "--project", pullProject, "--if-pulled", "--check"); err == nil {
		t.Errorf("--if-pulled --check on a stale namespace succeeded:\n%s", out)
	}
	if out, err := pullCLI(t, changed.URL, repo, "--project", pullProject, "--if-pulled", "--quiet"); err != nil || out != "" {
		t.Fatalf("--if-pulled --quiet on a changed set: err %v, stdout %q", err, out)
	}
	if got := readPulled(t, repo, pLintSkill); got != "# Lint v2\n" {
		t.Errorf("--if-pulled did not write the changed set: %q", got)
	}
}

// Criterion 3: --quiet with --dry-run is misuse (exit 2) with the exact
// message, writes nothing and sends no request.
func TestPullQuietWithDryRunIsMisuse(t *testing.T) {
	srv, rec := countingPullServer(t, absContent(t))
	repo := gitCheckout(t, userFiles())
	before := snapshot(t, repo)
	for _, args := range [][]string{{"--quiet", "--dry-run"}, {"--quiet", "--dry-run", "--if-pulled"}} {
		out, err := pullCLI(t, srv.URL, repo, append([]string{"--project", pullProject}, args...)...)
		var ue *cli.UsageError
		if !errors.As(err, &ue) {
			t.Fatalf("%v: want a usage error (exit 2), got %v\n%s", args, err, out)
		}
		if err.Error() != "--quiet and --dry-run cannot be used together" {
			t.Errorf("%v: message %q", args, err.Error())
		}
	}
	if bundleCalls(rec) != 0 {
		t.Errorf("misuse sent a request")
	}
	if after := snapshot(t, repo); !mapsEqual(before, after) {
		t.Errorf("misuse changed the project")
	}
}

// Criterion 4: a plain goldie pull is as before, including the first pull into
// a fresh repository: it writes the namespace and says so.
func TestPlainPullStillWorksIntoAFreshRepository(t *testing.T) {
	srv := pullServer(t, absContent(t))
	repo := gitCheckout(t, userFiles())
	out := pullShop(t, srv.URL, repo)
	if !strings.Contains(out, "Pulled ") {
		t.Errorf("a plain first pull printed %q, want the Pulled line", out)
	}
	wantSet(t, "pulled", namespaceFiles(snapshot(t, repo)), shopSet(), shopAgents)
	if out := pullShop(t, srv.URL, repo); !strings.Contains(out, "already up to date") {
		t.Errorf("a plain second pull printed %q", out)
	}
}
