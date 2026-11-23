package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #412 at the HTTP seam: goldie.skill.gets counts the
// skill gets that hit, with the attributes name, scope and via, and nothing
// else. A get that answers 404, an agent get and a request a guard rejects
// are not counted, so a name a caller makes up never becomes a label value.
// via is read from the client header: goldie/<version> from the CLI is "cli",
// the header goldie mcp sends is "mcp", and a missing or unknown one is
// "other". The export is read from an in-process OTLP collector, as for #456.

const gets412Project = "github.com/acme/planted-project-marker-6120"

// skillGetsContent writes a content directory with the global skill deploy,
// the Project skill shopcheck of gets412Project and the agent reviewer.
func skillGetsContent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range map[string]string{
		"global/skills/deploy/SKILL.md":                             "# Deploy\n",
		"global/agents/reviewer.md":                                 "---\nname: reviewer\ndescription: Reviews code\n---\nBody\n",
		"projects/" + gets412Project + "/skills/shopcheck/SKILL.md": "# Shopcheck\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSkillGetsAreCountedByNameScopeAndViaOnHitsOnly(t *testing.T) {
	col := newOTLPCollector(t)
	env := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": col.srv.URL,
		"OTEL_TRACES_EXPORTER":        "none",
		"OTEL_LOGS_EXPORTER":          "none",
		"OTEL_METRICS_EXPORTER":       "otlp",
	}
	tel, err := platform.SetupTelemetry(context.Background(), func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	cfg := platform.Config{ContentDir: skillGetsContent(t), APIToken: testToken, RateLimit: roomyRateLimit}
	pub, err := PublicHandlerWith(cfg, nil, tel)
	if err != nil {
		t.Fatalf("PublicHandlerWith: %v", err)
	}
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)

	withClient := func(client string) func(*http.Request) {
		return func(r *http.Request) {
			bearer(testToken)(r)
			if client != "" {
				r.Header.Set(clientHeader, client)
			}
		}
	}
	const missName = "planted-skill-marker-8841"
	for _, c := range []struct {
		path   string
		mutate func(*http.Request)
		status int
	}{
		// Hits, global Scope: no header, a foreign client, a goldie-looking
		// value that is no goldie client, and a hostile long one are "other".
		{"/api/v1/skills/deploy", withClient(""), http.StatusOK},
		{"/api/v1/skills/deploy", withClient("curl/8.5.0"), http.StatusOK},
		{"/api/v1/skills/deploy", withClient("goldie-planted-via-marker-2209/1.0.0"), http.StatusOK},
		{"/api/v1/skills/deploy", withClient(strings.Repeat("g", 4096)), http.StatusOK},
		// The CLI's header.
		{"/api/v1/skills/deploy", withClient("goldie/1.2.3"), http.StatusOK},
		// A hit in the Project's Scope.
		{"/api/v1/skills/shopcheck?project=" + url.QueryEscape(gets412Project), withClient(""), http.StatusOK},
		// Not counted: a miss, an agent get, a skill outside the asked view.
		{"/api/v1/skills/" + missName, withClient("goldie/1.2.3"), http.StatusNotFound},
		{"/api/v1/skills/reviewer", withClient(""), http.StatusNotFound},
		{"/api/v1/agents/reviewer", withClient(""), http.StatusOK},
		{"/api/v1/skills/shopcheck", withClient(""), http.StatusNotFound},
		// Rejected before the Snapshot answered.
		{"/api/v1/skills/deploy", func(r *http.Request) { r.Header.Set(clientHeader, "goldie/1.2.3") }, http.StatusUnauthorized},
	} {
		if resp, body := do(t, http.MethodGet, srv.URL+c.path, c.mutate); resp.StatusCode != c.status {
			t.Fatalf("GET %s: status %d, want %d: %s", c.path, resp.StatusCode, c.status, body)
		}
	}
	// goldie mcp's own header: a get_skill tool call.
	cs := stdioSession(t, srv.URL)
	if text, isErr := call(t, cs, "get_skill", map[string]any{"name": "deploy"}); isErr {
		t.Fatalf("get_skill: %s", text)
	}

	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if err := tel.Shutdown(sctx); err != nil {
		t.Fatal(err)
	}
	raw := col.body("/v1/metrics")
	counts, keys := metricCounts(t, raw, "goldie.skill.gets", func(a map[string]string) string {
		return a["name"] + "|" + a["scope"] + "|" + a["via"]
	})
	want := map[string]int64{
		"deploy|global|other":     4,
		"deploy|global|cli":       1,
		"deploy|global|mcp":       1,
		"shopcheck|project|other": 1,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("goldie.skill.gets by name|scope|via = %v, want %v", counts, want)
	}
	if !reflect.DeepEqual(keys, []string{"name", "scope", "via"}) {
		t.Errorf("goldie.skill.gets attribute keys = %v, want name, scope and via only", keys)
	}
	for _, planted := range []string{missName, "planted-project-marker-6120", "planted-via-marker-2209", testToken} {
		if bytes.Contains(raw, []byte(planted)) {
			t.Errorf("the metrics export carries %q (PC-12)", planted)
		}
	}
}
