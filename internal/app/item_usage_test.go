package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #900 (slice of #32) at the HTTP seam:
// GET /api/v1/{articles,skills,agents,loops}/{name}/usage reports the uses of
// an Item on this replica from an in-process tally (metrics are off in these
// tests), counting the events goldie.item.uses counts.

type itemUsage struct {
	Source string         `json:"source"`
	Since  string         `json:"since"`
	Uses   map[string]int `json:"uses"`
}

// itemUsageServer starts the public handler on the #898 content with metrics
// off.
func itemUsageServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := platform.Config{ContentDir: itemUsesContent(t), APITokens: namedTokenLines(), RateLimit: roomyRateLimit}
	pub, err := PublicHandlerWith(cfg, nil, nil)
	if err != nil {
		t.Fatalf("PublicHandlerWith: %v", err)
	}
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	return srv
}

func getItemUsage(t *testing.T, srv *httptest.Server, path string) itemUsage {
	t.Helper()
	status, body := itemsGet(t, srv, readerToken, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var u itemUsage
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	return u
}

// Criterion: the response is {source, since, uses} with all three via keys,
// so an unused Item returns 0 for each, for every Kind.
func TestItemUsageOfAnUnusedItemReportsZeroForEveryVia(t *testing.T) {
	srv := itemUsageServer(t)
	before := time.Now().Add(-time.Minute)
	for _, p := range []string{
		"/api/v1/articles/kubernetes/usage",
		"/api/v1/skills/deploy/usage",
		"/api/v1/agents/reviewer/usage",
		"/api/v1/loops/nightly/usage",
	} {
		u := getItemUsage(t, srv, p)
		if u.Source != "this-replica" {
			t.Errorf("GET %s: source %q, want this-replica", p, u.Source)
		}
		since, err := time.Parse(time.RFC3339, u.Since)
		if err != nil || since.After(time.Now()) || since.Before(before) {
			t.Errorf("GET %s: since %q (%v), want the process start in RFC 3339", p, u.Since, err)
		}
		if want := map[string]int{"cli": 0, "mcp": 0, "other": 0}; !reflect.DeepEqual(u.Uses, want) {
			t.Errorf("GET %s: uses %v, want %v", p, u.Uses, want)
		}
	}
}

// Criterion: the counts come from a tally beside ItemUseCounter.Add and count
// the same events as goldie.item.uses: hits by client, not misses, and not a
// read of the usage itself.
func TestItemUsageCountsTheSameUsesAsTheMetricWithMetricsOff(t *testing.T) {
	srv := itemUsageServer(t)
	get := func(path string, mutate func(*http.Request)) {
		t.Helper()
		if resp, body := do(t, http.MethodGet, srv.URL+path, func(r *http.Request) {
			bearer(readerToken)(r)
			if mutate != nil {
				mutate(r)
			}
		}); resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s: status %d: %s", path, resp.StatusCode, body)
		}
	}
	asCLI := func(r *http.Request) { r.Header.Set(clientHeader, "goldie/1.2.3") }
	asMCP := func(r *http.Request) { r.Header.Set(contract.ViaHeader, contract.ViaMCP) }

	get("/api/v1/skills/deploy", asCLI)
	get("/api/v1/skills/deploy", asCLI)
	get("/api/v1/skills/deploy", asMCP)
	get("/api/v1/skills/deploy", nil)
	get("/api/v1/skills/deploy", func(r *http.Request) { r.Header.Set(clientHeader, "curl/8.5.0") })
	get("/api/v1/skills/not-there-marker-9921", asCLI) // a miss is no use
	get("/api/v1/articles/kubernetes", asCLI)

	want := map[string]int{"cli": 2, "mcp": 1, "other": 2}
	for range 2 { // reading the usage is no use of the Item
		if u := getItemUsage(t, srv, "/api/v1/skills/deploy/usage"); !reflect.DeepEqual(u.Uses, want) {
			t.Errorf("skill deploy uses %v, want %v", u.Uses, want)
		}
	}
	if u := getItemUsage(t, srv, "/api/v1/articles/kubernetes/usage"); !reflect.DeepEqual(u.Uses, map[string]int{"cli": 1, "mcp": 0, "other": 0}) {
		t.Errorf("article kubernetes uses %v, want one cli use", u.Uses)
	}
	if u := getItemUsage(t, srv, "/api/v1/agents/reviewer/usage"); !reflect.DeepEqual(u.Uses, map[string]int{"cli": 0, "mcp": 0, "other": 0}) {
		t.Errorf("agent reviewer uses %v, want none", u.Uses)
	}
}

// Criteria: an unknown Item gets 404 in the error envelope, and a caller
// without access to the Item's Project gets the same answer as for a missing
// Item. A Project's Item is readable in its own Project view.
func TestItemUsageOfAMissingItemOrAnInaccessibleProjectIsTheSame404(t *testing.T) {
	srv := itemUsageServer(t)

	status, missing := itemsGet(t, srv, readerToken, "/api/v1/loops/nope-marker-4417/usage")
	if status != http.StatusNotFound {
		t.Fatalf("usage of a missing loop: status %d, want 404: %s", status, missing)
	}
	if code, _ := errorHint(t, missing); code != "not_found" || bytes.Contains(missing, []byte("no such endpoint")) || bytes.Contains(missing, []byte("nope-marker-4417")) {
		t.Errorf("usage of a missing loop: code %q, want the Item's not_found envelope that echoes nothing: %s", code, missing)
	}
	for _, p := range []string{
		"/api/v1/loops/shop-release/usage",                                         // a Project's loop in the global view
		"/api/v1/articles/shop-guide/usage",                                        // a Project's article in the global view
		"/api/v1/loops/shop-release/usage?project=github.com/other/unregistered-x", // another Project's view
	} {
		status, body := itemsGet(t, srv, readerToken, p)
		if status != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404: %s", p, status, body)
		}
		if code, _ := errorHint(t, body); code != "not_found" || bytes.Contains(body, []byte("no such endpoint")) {
			t.Errorf("GET %s: code %q, want the Item's not_found envelope: %s", p, code, body)
		}
	}
	if u := getItemUsage(t, srv, "/api/v1/loops/shop-release/usage?project="+uses898Shop); u.Source != "this-replica" {
		t.Errorf("a Project loop in its Project view: source %q", u.Source)
	}
}

// Criterion: the endpoint is guarded as the per-kind reads are, and a project
// value that is no spelling is a 400.
func TestItemUsageNeedsAnAPITokenAndAValidProject(t *testing.T) {
	srv := itemUsageServer(t)
	if status, body := itemsGet(t, srv, "", "/api/v1/skills/deploy/usage"); status != http.StatusUnauthorized {
		t.Errorf("usage without a token: status %d, want 401: %s", status, body)
	}
	if status, body := itemsGet(t, srv, readerToken, "/api/v1/skills/deploy/usage?project=..%2Fetc"); status != http.StatusBadRequest {
		t.Errorf("usage with a bad project: status %d, want 400: %s", status, body)
	}
}
