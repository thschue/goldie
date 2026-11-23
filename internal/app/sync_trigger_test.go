package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// #6: GET /api/v1/sync shows each Source's sync status next to the warnings,
// and POST /api/v1/sync triggers a poll now: 202 with a hint, 429 while a poll
// runs, operator-only. A new revision on a git Source is served without
// waiting for the poll interval.
//
// Seam decisions the spec change must follow (additive to #371): the 200 body
// keeps "warnings" and gains "sources", an array with one object per Source
// (this server has exactly one), each with "revision" (empty or absent for a
// Source without revisions), "last_poll", "last_success" (RFC 3339) and
// "last_error" (absent or empty when the last poll succeeded). The 202 body is
// a JSON object with a non-empty string "hint".

type sourceStatus struct {
	Revision    string `json:"revision"`
	LastPoll    string `json:"last_poll"`
	LastSuccess string `json:"last_success"`
	LastError   string `json:"last_error"`
}

type syncBody struct {
	Warnings json.RawMessage `json:"warnings"`
	Sources  []sourceStatus  `json:"sources"`
}

func getSyncStatus(t *testing.T, url string) syncBody {
	t.Helper()
	resp, body := do(t, http.MethodGet, url+statusPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", statusPath, resp.StatusCode, body)
	}
	var got syncBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("sources = %+v, want exactly one: %s", got.Sources, body)
	}
	return got
}

func mustTime(t *testing.T, what, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("%s = %q, want an RFC 3339 time: %v", what, s, err)
	}
	return ts
}

// postSync sends POST /api/v1/sync with token.
func postSync(t *testing.T, url, token string) (*http.Response, []byte) {
	t.Helper()
	return do(t, http.MethodPost, url+statusPath, bearer(token))
}

func syncHint(t *testing.T, body []byte) string {
	t.Helper()
	var got struct {
		Hint any `json:"hint"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("202 body %s is not JSON: %v", body, err)
	}
	s, _ := got.Hint.(string)
	return s
}

// --- the spec ------------------------------------------------------------------

func TestServedSpecDeclaresTheSyncTrigger(t *testing.T) {
	srv := newServerWith(t, platform.Config{})
	resp, body := do(t, http.MethodGet, srv.URL+"/openapi.json", bearer(testToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/openapi.json: status %d", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[statusPath].(map[string]any)
	post, _ := item["post"].(map[string]any)
	if post == nil {
		t.Fatalf("the served spec has no POST %s", statusPath)
	}
	if post["operationId"] != "postSync" {
		t.Errorf("POST operationId = %v, want postSync", post["operationId"])
	}
	if post["x-goldie-role"] != "operator" {
		t.Errorf("POST %s x-goldie-role = %v, want operator", statusPath, post["x-goldie-role"])
	}
	if _, ok := post["requestBody"]; ok {
		t.Errorf("POST %s declares a request body; the trigger takes none", statusPath)
	}
	responses, _ := post["responses"].(map[string]any)
	for _, status := range []string{"202", "401", "403", "429"} {
		if contracttest.ResolveRef(doc, responses[status]) == nil {
			t.Errorf("POST %s declares no %s response", statusPath, status)
		}
	}
	for _, status := range []string{"401", "403", "429"} {
		r := contracttest.ResolveRef(doc, responses[status])
		c, _ := r["content"].(map[string]any)
		media, _ := c["application/json"].(map[string]any)
		schema, _ := media["schema"].(map[string]any)
		if schema["$ref"] != "#/components/schemas/ErrorEnvelope" {
			t.Errorf("POST %s %s body schema %v, want the ErrorEnvelope", statusPath, status, schema)
		}
	}

	// GET gains the per-Source status and keeps the warnings.
	status := contracttest.ResolveRef(doc, map[string]any{"$ref": "#/components/schemas/SyncStatus"})
	props, _ := status["properties"].(map[string]any)
	if props["warnings"] == nil || props["sources"] == nil {
		t.Fatalf("SyncStatus properties = %v, want warnings and sources", keys(props))
	}
	sources, _ := props["sources"].(map[string]any)
	items := contracttest.ResolveRef(doc, sources["items"])
	if items == nil {
		t.Fatalf("SyncStatus.sources has no item schema: %v", sources)
	}
	itemProps, _ := items["properties"].(map[string]any)
	for _, f := range []string{"revision", "last_poll", "last_success", "last_error"} {
		if itemProps[f] == nil {
			t.Errorf("a Source's sync status has no %q: %v", f, keys(itemProps))
		}
	}
}

// --- GET: per-Source status ----------------------------------------------------

func TestSyncStatusShowsTheDirSourceAndKeepsTheWarnings(t *testing.T) {
	srv := warningServer(t, suppressContent)
	got := getSyncStatus(t, srv.URL)
	if string(got.Warnings) == "" || string(got.Warnings) == "[]" {
		t.Errorf("warnings = %s, want the existing warning kept", got.Warnings)
	}
	s := got.Sources[0]
	if s.LastError != "" {
		t.Errorf("last_error = %q after a good load, want none", s.LastError)
	}
	poll := mustTime(t, "last_poll", s.LastPoll)
	ok := mustTime(t, "last_success", s.LastSuccess)
	if poll.After(time.Now().Add(time.Minute)) || ok.After(time.Now().Add(time.Minute)) || ok.Before(time.Now().Add(-time.Hour)) {
		t.Errorf("last_poll %v and last_success %v are not recent", poll, ok)
	}
}

func TestSyncStatusIsStillOperatorOnlyWithSources(t *testing.T) {
	srv := warningServer(t, suppressContent)
	for name, auth := range map[string]func(*http.Request){"reader": bearer(readerToken), "no token": nil} {
		resp, body := do(t, http.MethodGet, srv.URL+statusPath, auth)
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401 or 403", name, resp.StatusCode)
		}
		if strings.Contains(string(body), "last_poll") || strings.Contains(string(body), "revision") {
			t.Errorf("%s: the refusal shows sync status: %s", name, body)
		}
	}
}

// gitSyncServer serves a git Source at repo's URL, polled only every hour so
// nothing but POST /api/v1/sync can pull a new revision, with the named
// reader and operator tokens. It returns once the first load is served.
func gitSyncServer(t *testing.T, repo *bareRepo) *httptest.Server {
	t.Helper()
	cfg := gitConfig(t, repo.url, time.Hour)
	cfg.GitAllowFileURL = true
	cfg.APIToken = ""
	cfg.APITokens = namedTokenLines()
	g := startGit(t, cfg)
	g.readyOnGit(t)
	srv := httptest.NewServer(g.pub)
	t.Cleanup(srv.Close)
	return srv
}

func TestSyncStatusShowsTheGitSourceRevision(t *testing.T) {
	repo := newBareRepo(t)
	first := repo.commit(gitArticle("git-first"))
	srv := gitSyncServer(t, repo)
	s := getSyncStatus(t, srv.URL).Sources[0]
	if s.Revision != first {
		t.Errorf("revision = %q, want the commit %q", s.Revision, first)
	}
	mustTime(t, "last_poll", s.LastPoll)
	mustTime(t, "last_success", s.LastSuccess)
	if s.LastError != "" {
		t.Errorf("last_error = %q, want none", s.LastError)
	}
}

// --- POST: trigger a poll ------------------------------------------------------

func TestPostSyncServesTheNewRevisionWithoutWaitingForThePollInterval(t *testing.T) {
	repo := newBareRepo(t)
	first := repo.commit(gitArticle("git-first"))
	srv := gitSyncServer(t, repo)
	before := getSyncStatus(t, srv.URL).Sources[0]

	second := repo.commit(with(gitArticle("git-first"), gitArticle("git-second")))
	if r, _ := do(t, http.MethodGet, srv.URL+"/api/v1/articles/git-second", bearer(operatorToken)); r.StatusCode != http.StatusNotFound {
		t.Fatalf("git-second served before any poll: %d", r.StatusCode)
	}

	resp, body := postSync(t, srv.URL, operatorToken)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s = %d, want 202: %s", statusPath, resp.StatusCode, body)
	}
	if syncHint(t, body) == "" {
		t.Errorf("202 body %s has no hint", body)
	}

	// Far below the one-hour interval: only the POST can have triggered it.
	eventually(t, 10*time.Second, "the new revision was never served", func() bool {
		r, _ := do(t, http.MethodGet, srv.URL+"/api/v1/articles/git-second", bearer(operatorToken))
		return r.StatusCode == http.StatusOK
	})
	after := getSyncStatus(t, srv.URL).Sources[0]
	if after.Revision != second || after.Revision == first {
		t.Errorf("revision = %q, want %q", after.Revision, second)
	}
	if !mustTime(t, "last_poll", after.LastPoll).After(mustTime(t, "last_poll", before.LastPoll)) {
		t.Errorf("last_poll did not advance: %q then %q", before.LastPoll, after.LastPoll)
	}
	if !mustTime(t, "last_success", after.LastSuccess).After(mustTime(t, "last_success", before.LastSuccess)) {
		t.Errorf("last_success did not advance: %q then %q", before.LastSuccess, after.LastSuccess)
	}
}

func TestPostSyncOfAnUnchangedRepositoryKeepsServing(t *testing.T) {
	repo := newBareRepo(t)
	first := repo.commit(gitArticle("git-first"))
	srv := gitSyncServer(t, repo)
	before := mustTime(t, "last_poll", getSyncStatus(t, srv.URL).Sources[0].LastPoll)
	resp, body := postSync(t, srv.URL, operatorToken)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST = %d, want 202: %s", resp.StatusCode, body)
	}
	eventually(t, 10*time.Second, "last_poll never advanced", func() bool {
		return mustTime(t, "last_poll", getSyncStatus(t, srv.URL).Sources[0].LastPoll).After(before)
	})
	if got := getSyncStatus(t, srv.URL).Sources[0]; got.Revision != first || got.LastError != "" {
		t.Errorf("status = %+v, want revision %q and no error", got, first)
	}
}

// A failed poll keeps the last good content and last_success, and last_error
// is safe: no host path, no URL, no credential; a content file is named
// relative to the Source root.
func TestFailedSyncShowsASafeLastErrorAndKeepsTheLastGoodRevision(t *testing.T) {
	repo := newBareRepo(t)
	first := repo.commit(gitArticle("git-first"))
	srv := gitSyncServer(t, repo)
	good := getSyncStatus(t, srv.URL).Sources[0]

	repo.commit(with(gitArticle("git-first"), map[string]string{"global/articles/sub/bad.md": "no front matter"}))
	if resp, body := postSync(t, srv.URL, operatorToken); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST = %d: %s", resp.StatusCode, body)
	}
	eventually(t, 10*time.Second, "last_error never appeared", func() bool {
		return getSyncStatus(t, srv.URL).Sources[0].LastError != ""
	})
	_, body := do(t, http.MethodGet, srv.URL+statusPath, bearer(operatorToken))
	s := getSyncStatus(t, srv.URL).Sources[0]
	if !strings.Contains(s.LastError, "global/articles/sub/bad.md") {
		t.Errorf("last_error = %q, want it to name global/articles/sub/bad.md relative to the root", s.LastError)
	}
	for _, leak := range []string{repo.dir, repo.url, "file://", os.TempDir(), "/home", "/Users"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("the sync status leaks %q: %s", leak, body)
		}
	}
	if s.Revision != first || s.LastSuccess != good.LastSuccess {
		t.Errorf("revision %q last_success %q, want the last good %q %q", s.Revision, s.LastSuccess, first, good.LastSuccess)
	}
	mustTime(t, "last_poll", s.LastPoll)
	if r, _ := do(t, http.MethodGet, srv.URL+"/api/v1/articles/git-first", bearer(operatorToken)); r.StatusCode != http.StatusOK {
		t.Errorf("the last good content is not served: %d", r.StatusCode)
	}

	// A good commit clears last_error.
	repo.commit(gitArticle("git-first"))
	postSync(t, srv.URL, operatorToken)
	eventually(t, 10*time.Second, "last_error never cleared", func() bool {
		return getSyncStatus(t, srv.URL).Sources[0].LastError == ""
	})
}

func TestFailedSyncWithAVanishedRepositoryLeaksNoPath(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	srv := gitSyncServer(t, repo)
	if err := os.RemoveAll(repo.dir); err != nil {
		t.Fatal(err)
	}
	postSync(t, srv.URL, operatorToken)
	eventually(t, 10*time.Second, "last_error never appeared", func() bool {
		return getSyncStatus(t, srv.URL).Sources[0].LastError != ""
	})
	_, body := do(t, http.MethodGet, srv.URL+statusPath, bearer(operatorToken))
	for _, leak := range []string{repo.dir, repo.url, "file://"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("the sync status leaks %q: %s", leak, body)
		}
	}
}

// --- POST: roles, and one poll at a time ---------------------------------------

// countingSource serves the test content and counts its loads; once block is
// called, every load waits until the returned channel closes or its context
// ends.
type countingSource struct {
	loads atomic.Int32
	mu    sync.Mutex
	hold  chan struct{}
}

func (c *countingSource) Load(ctx context.Context) (*engine.Snapshot, error) {
	c.loads.Add(1)
	c.mu.Lock()
	hold := c.hold
	c.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return engine.NewDirSource(testContent).Load(ctx)
}

func (c *countingSource) block() chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hold = make(chan struct{})
	return c.hold
}

// blockableSyncServer serves a countingSource started like goldie server starts a
// Source, polled only every hour, with the named reader and operator tokens.
func blockableSyncServer(t *testing.T) (*httptest.Server, *countingSource) {
	t.Helper()
	cfg := publicConfig(t)
	cfg.APIToken = ""
	cfg.APITokens = namedTokenLines()
	tokens, err := apiTokens(cfg)
	if err != nil {
		t.Fatal(err)
	}
	src := &countingSource{}
	pub, err := newPublic(cfg, tokens, source{syncer: engine.NewSyncer(src), check: sourceDirCheck}, nil, api.DefaultActivityKeepalive)
	if err != nil {
		t.Fatal(err)
	}
	pub.poll = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startReturns(t, pub, ctx, platform.NewRegistry(0))
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	eventually(t, 5*time.Second, "the poll loop never made its first poll", func() bool {
		return src.loads.Load() >= 1 && pub.syncer.Current() != nil
	})
	return srv, src
}

func TestPostSyncRefusesAReaderWithoutPolling(t *testing.T) {
	srv, src := blockableSyncServer(t)
	loads := src.loads.Load()
	for name, c := range map[string]struct {
		auth   func(*http.Request)
		status int
	}{
		"reader":    {bearer(readerToken), http.StatusForbidden},
		"hashed":    {bearer(hashedSecret), http.StatusForbidden},
		"no token":  {nil, http.StatusUnauthorized},
		"bad token": {bearer("wrong-token-presented-here"), http.StatusUnauthorized},
	} {
		resp, body := do(t, http.MethodPost, srv.URL+statusPath, c.auth)
		if resp.StatusCode != c.status {
			t.Errorf("%s: status %d, want %d: %s", name, resp.StatusCode, c.status, body)
		}
		if c.status == http.StatusForbidden {
			if code, hint := errorHint(t, body); code == "" || hint != "Use a token with the operator role." {
				t.Errorf("%s: code %q hint %q, want the operator-role hint", name, code, hint)
			}
		}
	}
	time.Sleep(200 * time.Millisecond)
	if got := src.loads.Load(); got != loads {
		t.Errorf("a refused POST started %d poll(s)", got-loads)
	}
}

func TestPostSyncAnswers429WhileAPollRunsAndStartsNoSecondPoll(t *testing.T) {
	srv, src := blockableSyncServer(t)
	release := src.block()
	base := src.loads.Load()

	resp, body := postSync(t, srv.URL, operatorToken)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first POST = %d, want 202: %s", resp.StatusCode, body)
	}
	eventually(t, 5*time.Second, "the triggered poll never started", func() bool { return src.loads.Load() == base+1 })

	for range 3 {
		resp, body = postSync(t, srv.URL, operatorToken)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("POST during a poll = %d, want 429: %s", resp.StatusCode, body)
		}
		if code, hint := errorHint(t, body); code == "" || hint == "" {
			t.Errorf("429 code %q hint %q, want both", code, hint)
		}
	}
	// The status stays readable while a poll runs.
	getSyncStatus(t, srv.URL)
	if got := src.loads.Load(); got != base+1 {
		t.Errorf("loads = %d, want %d: a 429 must not start a poll", got, base+1)
	}

	close(release)
	eventually(t, 5*time.Second, "POST never accepted again after the poll ended", func() bool {
		r, _ := postSync(t, srv.URL, operatorToken)
		return r.StatusCode == http.StatusAccepted
	})
}
