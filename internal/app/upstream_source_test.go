package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #391: an upstream Source combines a repository's
// .goldie/ with a central server. The embedded server's base is the
// Project's resolved view from the upstream (GET /api/v1/projects/{id}/items,
// #388), with .goldie/ layered on top (#389). Maintainer decisions on #391:
//
//  1. The token goes only to an address with the same origin as GOLDIE_ADDR.
//     A .goldie/config.yaml addr of another origin is refused and never
//     receives it.
//  2. When upstream is unreachable at startup the server still starts, admin
//     /readyz fails source:upstream and the API answers 503 not_ready until
//     the first good load. A later failure keeps the last good Snapshot and
//     readiness stays ok.
//
// The seam these tests fix, where the issue names none: Local and
// LocalOptions take an Upstream value,
//
//	type Upstream struct {
//	    Addr    string // GOLDIE_ADDR, the central server's base URL; "" is no upstream
//	    Token   string // GOLDIE_TOKEN, sent to the server at Addr only
//	    Project string // the Project whose view is fetched, host/org/repo
//	}
//
// and Local has OnListen func(listener, addr string), called once per
// listener ("app", "admin") as it binds, so a test can reach /readyz. The
// upstream is a real goldie server over testdata/items-content, which holds
// the global Items platform/deploy, platform/policy (enforced), style and
// global-only, and the Project github.com/acme/shop with runbook, shop-rule
// (enforced) and its own platform/deploy.

const (
	upLocalOnly = "/api/v1/articles/team%2Fonly-local"
	upRunbook   = "/api/v1/articles/runbook?project=" + shopProject
	upGlobal    = "/api/v1/articles/global-only?project=" + shopProject
	upDeploy    = "/api/v1/articles/platform%2Fdeploy?project=" + shopProject
)

// upstreamServer serves itemsContent behind a switch: while fail is set every
// request answers 500. authed counts the requests that carried the token.
func upstreamServer(t *testing.T) (srv *httptest.Server, fail *atomic.Bool, authed *atomic.Int32) {
	t.Helper()
	h, err := PublicHandler(platform.Config{ContentDir: itemsContent, APIToken: testToken, RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	fail, authed = &atomic.Bool{}, &atomic.Int32{}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+testToken {
			authed.Add(1)
		}
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, fail, authed
}

// tokenSpy is a server that counts every request it gets, with or without
// the token, so a test can show nothing reached it.
func tokenSpy(t *testing.T) (srv *httptest.Server, requests *atomic.Int32) {
	t.Helper()
	requests = &atomic.Int32{}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "spy", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

// upRepo is a repository whose .goldie holds team/only-local and a
// platform/deploy that shadows the upstream's, plus config.yaml with addr
// when addr is not empty, plus the extra files.
func upRepo(t *testing.T, addr string, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		".goldie/articles/team/only-local.md": "---\nname: Only local\nkeywords: [onlylocal]\n---\nLOCAL-ONLY-BODY\n",
		".goldie/articles/platform/deploy.md": "---\nname: Local deploy\nkeywords: [kubernetes]\n---\nLOCAL-DEPLOY-BODY\n",
	}
	if addr != "" {
		files[".goldie/config.yaml"] = "addr: " + addr + "\n"
	}
	for k, v := range extra {
		files[k] = v
	}
	return optsWriteTree(t, files)
}

// upRunning is a started embedded server.
type upRunning struct {
	addr, token, admin string
}

// upStart runs a Local over the repository's .goldie with the upstream and a
// fast poll, and returns once the listeners are bound.
func upStart(t *testing.T, repo string, up Upstream) (*upRunning, error) {
	t.Helper()
	cfg, err := platform.LoadConfig(noEnv)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DrainTimeout = 0
	cfg.PollInterval = 50 * time.Millisecond
	oldFloor := minPollInterval
	minPollInterval = time.Millisecond
	t.Cleanup(func() { minPollInterval = oldFloor })
	admin := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addr, token, wait, err := Local{
		Config: cfg, GoldieDir: filepath.Join(repo, ".goldie"), Upstream: up,
		OnListen: func(listener, a string) {
			if listener == "admin" {
				admin <- a
			}
		},
	}.Start(ctx)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { cancel(); _ = wait() })
	return &upRunning{addr: addr, token: token, admin: <-admin}, nil
}

func upMustStart(t *testing.T, repo string, up Upstream) *upRunning {
	t.Helper()
	r, err := upStart(t, repo, up)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return r
}

func (r *upRunning) get(t *testing.T, path string) (int, string) {
	t.Helper()
	return optsGet(t, r.addr, r.token, path)
}

// waitReady waits for the first good upstream load.
func (r *upRunning) waitReady(t *testing.T) {
	t.Helper()
	eventually(t, 5*time.Second, "the upstream was never loaded", func() bool {
		code, _ := r.get(t, upGlobal)
		return code == http.StatusOK
	})
}

// sourceUpstream returns the /readyz status code, body and the
// source:upstream check's status ("" when it is not listed).
func (r *upRunning) sourceUpstream(t *testing.T) (int, string, string) {
	t.Helper()
	code, raw, checks := readyz(t, r.admin)
	for _, c := range checks {
		if c.Name == "source:upstream" {
			return code, raw, c.Status
		}
	}
	return code, raw, ""
}

// assertNotReady checks that path answers 503 not_ready.
func (r *upRunning) assertNotReady(t *testing.T, path string) {
	t.Helper()
	if code, body := r.get(t, path); code != http.StatusServiceUnavailable || !strings.Contains(body, `"not_ready"`) {
		t.Errorf("GET %s = %d %s, want 503 not_ready", path, code, body)
	}
}

// With an upstream the base is the upstream's view of the Project, with the
// repository's .goldie on top: a local Item shadows an upstream one.
func TestUpstreamBaseWithTheLocalScopeOnTop(t *testing.T) {
	up, _, authed := upstreamServer(t)
	r := upMustStart(t, upRepo(t, "", nil), Upstream{Addr: up.URL, Token: testToken, Project: shopProject})
	r.waitReady(t)
	if authed.Load() == 0 {
		t.Error("the upstream never saw the token")
	}
	if code, body := r.get(t, upRunbook); code != http.StatusOK || !strings.Contains(body, "runbook") {
		t.Errorf("GET upstream Project Item = %d %q, want 200", code, body)
	}
	if code, body := r.get(t, upLocalOnly+"?project="+shopProject); code != http.StatusOK || !strings.Contains(body, "LOCAL-ONLY-BODY") {
		t.Errorf("GET local Item = %d %q, want 200", code, body)
	}
	code, body := r.get(t, upDeploy)
	if code != http.StatusOK || !strings.Contains(body, "LOCAL-DEPLOY-BODY") || strings.Contains(body, "shop-deploy-body") {
		t.Errorf("GET shadowed Item = %d %q, want the local copy over the upstream's", code, body)
	}
}

// A .goldie/config.yaml addr with the origin of GOLDIE_ADDR is the upstream,
// and gets the token. A trailing slash does not make another origin.
func TestUpstreamConfigAddrOfTheSameOriginIsUsed(t *testing.T) {
	up, _, authed := upstreamServer(t)
	r := upMustStart(t, upRepo(t, up.URL+"/", nil), Upstream{Addr: up.URL, Token: testToken, Project: shopProject})
	r.waitReady(t)
	if authed.Load() == 0 {
		t.Error("the upstream at the config.yaml addr never saw the token")
	}
}

// A cloned repository naming another origin never receives the token: the
// server refuses to start, with a hint to set GOLDIE_ADDR, and the other
// origin is not contacted at all.
func TestUpstreamConfigAddrOfAnotherOriginIsRefusedAndNeverContacted(t *testing.T) {
	up, _, _ := upstreamServer(t)
	spy, requests := tokenSpy(t)
	_, err := upStart(t, upRepo(t, spy.URL, nil), Upstream{Addr: up.URL, Token: testToken, Project: shopProject})
	if err == nil {
		t.Fatal("Start accepted a config.yaml addr of another origin")
	}
	if !strings.Contains(err.Error(), platform.EnvAddr) {
		t.Errorf("error %q has no hint to set %s", err, platform.EnvAddr)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error %q leaks the token", err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := requests.Load(); n != 0 {
		t.Errorf("the other origin got %d requests, want none: the token must never leave GOLDIE_ADDR's origin", n)
	}
}

// A config.yaml the strict reader rejects (here an unknown key next to a valid
// same-origin addr) fails Start naming config.yaml; the upstream is not
// contacted, so no token goes out on the strength of a file that is not
// understood.
func TestUpstreamConfigWithAnUnknownKeyFailsStartAndIsNeverContacted(t *testing.T) {
	up, requests := tokenSpy(t)
	repo := upRepo(t, "", map[string]string{
		".goldie/config.yaml": "addr: " + up.URL + "\nsurprise: true\n",
	})
	_, err := upStart(t, repo, Upstream{Addr: up.URL, Token: testToken, Project: shopProject})
	if err == nil {
		t.Fatal("Start accepted a config.yaml with an unknown key")
	}
	if !strings.Contains(err.Error(), "config.yaml") {
		t.Errorf("error %q does not name config.yaml", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error %q leaks the token", err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := requests.Load(); n != 0 {
		t.Errorf("the upstream got %d requests, want none", n)
	}
}

// A local Item that shadows an enforced upstream Item stops startup and
// names the Item and the local file, not a host path, the address or the
// token.
func TestUpstreamEnforcedItemCannotBeShadowedLocally(t *testing.T) {
	up, _, _ := upstreamServer(t)
	repo := upRepo(t, "", map[string]string{
		".goldie/articles/platform/policy.md": "---\nname: Mine\nkeywords: [policy]\n---\nMY-POLICY\n",
	})
	_, err := upStart(t, repo, Upstream{Addr: up.URL, Token: testToken, Project: shopProject})
	if err == nil {
		t.Fatal("Start succeeded with a local Item shadowing an enforced upstream Item")
	}
	msg := err.Error()
	for _, want := range []string{"article:platform/policy", ".goldie/articles/platform/policy.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not name %s", msg, want)
		}
	}
	for _, leak := range []string{repo, testToken, up.URL} {
		if strings.Contains(msg, leak) {
			t.Errorf("error %q leaks %q", msg, leak)
		}
	}
}

// An enforced Item of the Project (shop-rule) is enforced too, not only a
// global one.
func TestUpstreamEnforcedProjectItemCannotBeShadowedLocally(t *testing.T) {
	up, _, _ := upstreamServer(t)
	repo := upRepo(t, "", map[string]string{
		".goldie/articles/shop-rule.md": "---\nname: Mine\nkeywords: [rule]\n---\nMY-RULE\n",
	})
	_, err := upStart(t, repo, Upstream{Addr: up.URL, Token: testToken, Project: shopProject})
	if err == nil || !strings.Contains(err.Error(), "article:shop-rule") {
		t.Fatalf("Start error = %v, want it to name article:shop-rule", err)
	}
}

// http is refused unless the host is loopback, before any connection.
func TestUpstreamOverPlainHTTPToAnotherHostIsRefused(t *testing.T) {
	began := time.Now()
	_, err := upStart(t, upRepo(t, "", nil), Upstream{Addr: "http://192.0.2.1:9", Token: testToken, Project: shopProject})
	if err == nil {
		t.Fatal("Start accepted an http upstream that is not loopback")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error %q does not say https is required", err)
	}
	if d := time.Since(began); d > 3*time.Second {
		t.Errorf("refusal took %v: it must not try to connect", d)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error %q leaks the token", err)
	}
}

// An unreachable upstream at startup does not stop the server. Readiness
// fails source:upstream and the API answers 503 not_ready until the first
// good load, then it answers and readiness is ok.
func TestUpstreamUnreachableAtStartupStartsNotReadyUntilTheFirstGoodLoad(t *testing.T) {
	up, fail, _ := upstreamServer(t)
	fail.Store(true)
	r := upMustStart(t, upRepo(t, "", nil), Upstream{Addr: up.URL, Token: testToken, Project: shopProject})

	code, raw, status := r.sourceUpstream(t)
	if code != http.StatusServiceUnavailable || status == "" || status == "ok" {
		t.Errorf("/readyz before the first load = %d %s, want 503 with source:upstream not ok", code, raw)
	}
	for _, p := range []string{upLocalOnly, upGlobal, "/api/v1/search?q=onlylocal"} {
		r.assertNotReady(t, p)
	}
	for _, leak := range []string{testToken, up.URL, "boom"} {
		if strings.Contains(raw, leak) {
			t.Errorf("/readyz echoes %q: %s", leak, raw)
		}
	}

	fail.Store(false)
	r.waitReady(t)
	eventually(t, 5*time.Second, "source:upstream never became ok", func() bool {
		code, _, status := r.sourceUpstream(t)
		return code == http.StatusOK && status == "ok"
	})
}

// A later upstream failure keeps the last good Snapshot, and readiness stays
// ok, as for a stale git Source (PC-02).
func TestUpstreamLaterFailureKeepsTheLastGoodSnapshot(t *testing.T) {
	up, fail, authed := upstreamServer(t)
	r := upMustStart(t, upRepo(t, "", nil), Upstream{Addr: up.URL, Token: testToken, Project: shopProject})
	r.waitReady(t)

	fail.Store(true)
	seen := authed.Load()
	eventually(t, 5*time.Second, "the poll loop never asked the failing upstream", func() bool { return authed.Load() >= seen+2 })
	if code, body := r.get(t, upRunbook); code != http.StatusOK || !strings.Contains(body, "runbook") {
		t.Errorf("GET after the upstream failed = %d %q, want the last good Item", code, body)
	}
	if code, raw, status := r.sourceUpstream(t); code != http.StatusOK || status != "ok" {
		t.Errorf("/readyz while stale = %d %s, want 200 with source:upstream ok", code, raw)
	}
}

// An upstream that answers with a redirect is not followed: the token goes
// nowhere else, and the load fails (not ready).
func TestUpstreamRedirectIsNotFollowed(t *testing.T) {
	spy, requests := tokenSpy(t)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, spy.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redir.Close)
	r := upMustStart(t, upRepo(t, "", nil), Upstream{Addr: redir.URL, Token: testToken, Project: shopProject})
	time.Sleep(300 * time.Millisecond)
	if n := requests.Load(); n != 0 {
		t.Errorf("the redirect target got %d requests, want none", n)
	}
	r.assertNotReady(t, upGlobal)
}

// A response over the size cap is refused whole: the load fails.
func TestUpstreamResponseOverTheSizeCapIsRefused(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"big","name":"Big","keywords":["big"],"status":"published","scope":"global","enforced":false,"content":"`))
		chunk := []byte(strings.Repeat("a", 1<<16))
		for n := 0; n <= contract.MaxResponseBytes; n += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(`"}],"suppressions":[]}`))
	}))
	t.Cleanup(big.Close)
	r := upMustStart(t, upRepo(t, "", nil), Upstream{Addr: big.URL, Token: testToken, Project: shopProject})
	time.Sleep(300 * time.Millisecond)
	r.assertNotReady(t, "/api/v1/articles/big")
}

// An upstream cannot hand over an Item of Scope local, a path as a Scope or a
// malformed enforced: the load fails instead of serving it.
func TestUpstreamResponseWithAnInvalidScopeOrEnforcedIsRefused(t *testing.T) {
	for name, item := range map[string]string{
		"scope local":      `{"id":"x","name":"X","keywords":["x"],"status":"published","scope":"local","enforced":false,"content":"c"}`,
		"enforced a text":  `{"id":"x","name":"X","keywords":["x"],"status":"published","scope":"global","enforced":"yes","content":"c"}`,
		"scope not a name": `{"id":"x","name":"X","keywords":["x"],"status":"published","scope":"../../etc","enforced":false,"content":"c"}`,
	} {
		t.Run(name, func(t *testing.T) {
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"items":[` + item + `],"suppressions":[]}`))
			}))
			t.Cleanup(bad.Close)
			r := upMustStart(t, upRepo(t, "", nil), Upstream{Addr: bad.URL, Token: testToken, Project: shopProject})
			time.Sleep(300 * time.Millisecond)
			r.assertNotReady(t, "/api/v1/articles/x")
		})
	}
}
