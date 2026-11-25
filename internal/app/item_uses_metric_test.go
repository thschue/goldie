package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #898 (slice of #31) at the HTTP and MCP seams:
// goldie.item.uses counts every answered lookup (article) and get (skill,
// agent, loop) with the attributes kind, name, scope, via, project and
// principal, and nothing else. via is cli, mcp or other; project is the
// registered spelling of a registered Project or "unregistered"; principal is
// the name of the token that made the request. A 404, a request a guard
// rejects and a name a caller makes up never reach a label, and the
// instrument keeps the 2000 combinations every metric keeps. The export is
// read from an in-process OTLP collector, as for #412.

const (
	uses898Shop     = "github.com/Acme/Shop"
	uses898Reader   = "ci-reader"
	uses898Operator = "ci-operator"
	uses898MCP      = "mcp-agent"
)

// itemUsesContent writes a content directory with the global article
// kubernetes, skill deploy, agent reviewer and loop nightly, and the Project
// github.com/Acme/Shop with the article shop-guide and the loop shop-release.
func itemUsesContent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"global/articles/kubernetes.md":                       "---\nname: Kubernetes basics\nkeywords: [kubernetes]\n---\nGlobal guidance on Kubernetes.\n",
		"global/skills/deploy/SKILL.md":                       "# Deploy\n",
		"global/agents/reviewer.md":                           loopAgent("reviewer", "global reviewer"),
		"global/loops/nightly.md":                             loopFile("nightly", "30m", []string{"reviewer"}, []string{"deploy"}, "when CI is green"),
		"projects/" + uses898Shop + "/articles/shop-guide.md": "---\nname: Shop guide\nkeywords: [shop]\n---\nShop guidance.\n",
		"projects/" + uses898Shop + "/loops/shop-release.md":  loopFile("shop-release", "1h", []string{"reviewer"}, []string{"deploy"}, "when released"),
	} {
		writeRaw(t, dir, name, content)
	}
	return dir
}

// itemUsesServer starts the public handler on content with tokens (a
// GOLDIE_API_TOKENS value) and metrics exported to an in-process collector.
func itemUsesServer(t *testing.T, tokens string) (*httptest.Server, *platform.Telemetry, *otlpCollector) {
	t.Helper()
	col := newOTLPCollector(t)
	env := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": col.srv.URL,
		"OTEL_TRACES_EXPORTER":        "none",
		"OTEL_LOGS_EXPORTER":          "none",
		"OTEL_METRICS_EXPORTER":       "otlp",
		"OTEL_METRIC_EXPORT_INTERVAL": "3600000",
	}
	tel, err := platform.SetupTelemetry(context.Background(), func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	cfg := platform.Config{ContentDir: itemUsesContent(t), APITokens: tokens, RateLimit: roomyRateLimit}
	pub, err := PublicHandlerWith(cfg, nil, tel)
	if err != nil {
		t.Fatalf("PublicHandlerWith: %v", err)
	}
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	return srv, tel, col
}

// itemUsesExport flushes tel and returns the export, the counts of
// goldie.item.uses by the point key, and the attribute keys it carried.
func itemUsesExport(t *testing.T, tel *platform.Telemetry, col *otlpCollector, point func(map[string]string) string) ([]byte, map[string]int64, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tel.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	raw := col.body("/v1/metrics")
	counts, keys := metricCounts(t, raw, "goldie.item.uses", point)
	return raw, counts, keys
}

func TestItemUsesAreCountedByKindNameScopeViaProjectAndPrincipalOnHitsOnly(t *testing.T) {
	srv, tel, col := itemUsesServer(t, namedTokenLines()+"\n"+uses898MCP+":reader:"+testToken)
	as := func(token, client string) func(*http.Request) {
		return func(r *http.Request) {
			bearer(token)(r)
			if client != "" {
				r.Header.Set(clientHeader, client)
			}
		}
	}
	const (
		missName      = "planted-item-marker-8841"
		plantedA      = "github.com/Planted/Project-Marker-7731"
		plantedB      = "github.com/other/planted-project-marker-7731"
		plantedClient = "goldie-planted-via-marker-2209/1.0.0"
	)
	for _, c := range []struct {
		path   string
		mutate func(*http.Request)
		status int
	}{
		// One hit of every counted Kind, with no client header, as the CLI
		// and with a foreign client.
		{"/api/v1/articles/kubernetes", as(readerToken, ""), http.StatusOK},
		{"/api/v1/skills/deploy", as(operatorToken, "goldie/1.2.3"), http.StatusOK},
		{"/api/v1/agents/reviewer", as(readerToken, "curl/8.5.0"), http.StatusOK},
		{"/api/v1/loops/nightly", as(operatorToken, "goldie/1.2.3"), http.StatusOK},
		// A registered Project, sent in another case, counts under its
		// registered spelling and its own Scope.
		{"/api/v1/articles/shop-guide?project=github.com/acme/shop", as(readerToken, ""), http.StatusOK},
		{"/api/v1/loops/shop-release?project=github.com/ACME/SHOP", as(readerToken, "goldie/1.2.3"), http.StatusOK},
		{"/api/v1/agents/reviewer?project=github.com/acme/shop", as(readerToken, ""), http.StatusOK},
		// Two spellings of unregistered Projects share one series; a
		// goldie-looking value that is no goldie client is "other".
		{"/api/v1/loops/nightly?project=" + plantedA, as(readerToken, plantedClient), http.StatusOK},
		{"/api/v1/loops/nightly?project=" + plantedB, as(readerToken, plantedClient), http.StatusOK},
		// Not counted: a miss of every Kind, a Project's loop in the global
		// view, and a request a guard rejects.
		{"/api/v1/articles/" + missName, as(readerToken, ""), http.StatusNotFound},
		{"/api/v1/skills/" + missName, as(readerToken, ""), http.StatusNotFound},
		{"/api/v1/agents/" + missName, as(readerToken, ""), http.StatusNotFound},
		{"/api/v1/loops/" + missName, as(readerToken, ""), http.StatusNotFound},
		{"/api/v1/loops/shop-release", as(readerToken, ""), http.StatusNotFound},
		{"/api/v1/skills/deploy", func(r *http.Request) { r.Header.Set(clientHeader, "goldie/1.2.3") }, http.StatusUnauthorized},
	} {
		if resp, body := do(t, http.MethodGet, srv.URL+c.path, c.mutate); resp.StatusCode != c.status {
			t.Fatalf("GET %s: status %d, want %d: %s", c.path, resp.StatusCode, c.status, body)
		}
	}
	// goldie mcp's own header, with a named token: a get_article tool call.
	cs := stdioSession(t, srv.URL)
	if text, isErr := call(t, cs, "get_article", map[string]any{"id": "kubernetes"}); isErr {
		t.Fatalf("get_article: %s", text)
	}

	raw, counts, keys := itemUsesExport(t, tel, col, func(a map[string]string) string {
		project := a["project"]
		if project == "" {
			project = "unregistered" // a request without a project is in no registered one
		}
		return strings.Join([]string{a["kind"], a["name"], a["scope"], a["via"], project, a["principal"]}, "|")
	})
	want := map[string]int64{
		"article|kubernetes|global|other|unregistered|" + uses898Reader:         1,
		"skill|deploy|global|cli|unregistered|" + uses898Operator:               1,
		"agent|reviewer|global|other|unregistered|" + uses898Reader:             1,
		"loop|nightly|global|cli|unregistered|" + uses898Operator:               1,
		"article|shop-guide|project|other|" + uses898Shop + "|" + uses898Reader: 1,
		"loop|shop-release|project|cli|" + uses898Shop + "|" + uses898Reader:    1,
		"agent|reviewer|global|other|" + uses898Shop + "|" + uses898Reader:      1,
		"loop|nightly|global|other|unregistered|" + uses898Reader:               2,
		"article|kubernetes|global|mcp|unregistered|" + uses898MCP:              1,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("goldie.item.uses by kind|name|scope|via|project|principal = %v, want %v", counts, want)
	}
	if !reflect.DeepEqual(keys, []string{"kind", "name", "principal", "project", "scope", "via"}) {
		t.Errorf("goldie.item.uses attribute keys = %v, want kind, name, scope, via, project and principal only", keys)
	}
	for _, planted := range []string{missName, "Project-Marker-7731", "planted-project-marker-7731", "planted-via-marker-2209", testToken, readerToken, operatorToken} {
		if bytes.Contains(raw, []byte(planted)) {
			t.Errorf("the metrics export carries %q (PC-12)", planted)
		}
	}
}

// Criterion: the labels are bounded. 2001 principals with one get each must
// leave the instrument at the 2000 combinations every metric keeps, with the
// overflow aggregated and nothing lost.
func TestItemUsesKeepAtMostTheMetricCardinalityLimit(t *testing.T) {
	const principals = 2001
	secret := func(i int) string { return fmt.Sprintf("%s%04d", strings.Repeat("s", 40), i) }
	lines := make([]string, 0, principals)
	for i := range principals {
		lines = append(lines, fmt.Sprintf("principal-%04d:reader:%s", i, secret(i)))
	}
	srv, tel, col := itemUsesServer(t, strings.Join(lines, "\n"))
	for i := range principals {
		if resp, body := do(t, http.MethodGet, srv.URL+"/api/v1/skills/deploy", bearer(secret(i))); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/v1/skills/deploy as principal-%04d: status %d: %s", i, resp.StatusCode, body)
		}
	}
	_, counts, _ := itemUsesExport(t, tel, col, func(a map[string]string) string { return a["principal"] })
	var total int64
	for _, n := range counts {
		total += n
	}
	if len(counts) == 0 || len(counts) > 2001 {
		t.Errorf("%d series for %d principals, want a bounded number (at most 2000 and one overflow point)", len(counts), principals)
	}
	if total != principals {
		t.Errorf("counted %d uses, want %d: overflow must be aggregated, not dropped", total, principals)
	}
}
