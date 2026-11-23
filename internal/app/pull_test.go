package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
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
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #16 (goldie pull: materialise the Goldie namespace),
// slice 1: the HTTP contract seam (GET /api/v1/bundle) against the real public
// handler. The goldie pull CLI tests are slice 2 (#594), at bc5ff95.
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
