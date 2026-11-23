package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// skillGetCounts serves the skill content with metrics export on, runs drive
// against it, and returns goldie.skill.gets as name|scope|via counts.
func skillGetCounts(t *testing.T, drive func(srvURL string)) map[string]int64 {
	t.Helper()
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
	pub, err := PublicHandlerWith(platform.Config{ContentDir: skillGetsContent(t), APIToken: testToken, RateLimit: roomyRateLimit}, nil, tel)
	if err != nil {
		t.Fatalf("PublicHandlerWith: %v", err)
	}
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	drive(srv.URL)
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tel.Shutdown(sctx); err != nil {
		t.Fatal(err)
	}
	counts, _ := metricCounts(t, col.body("/v1/metrics"), "goldie.skill.gets", func(a map[string]string) string {
		return a["name"] + "|" + a["scope"] + "|" + a["via"]
	})
	return counts
}

// A skill is matched ignoring case, so the metric labels the skill's own
// name: a caller's spelling never makes a series of its own.
func TestSkillGetsLabelTheCanonicalNameWhateverTheCase(t *testing.T) {
	counts := skillGetCounts(t, func(url string) {
		for _, name := range []string{"deploy", "DEPLOY", "Deploy", "dEpLoY"} {
			if resp, body := do(t, http.MethodGet, url+"/api/v1/skills/"+name, bearer(testToken)); resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status %d: %s", name, resp.StatusCode, body)
			}
		}
	})
	if want := map[string]int64{"deploy|global|other": 4}; !reflect.DeepEqual(counts, want) {
		t.Errorf("goldie.skill.gets = %v, want %v", counts, want)
	}
}

// The server's own /mcp counts as MCP.
func TestSkillGetThroughTheServersMCPCountsAsMCP(t *testing.T) {
	counts := skillGetCounts(t, func(url string) {
		cs := mustConnectHTTP(t, url+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
		if text, isErr := call(t, cs, "get_skill", map[string]any{"name": "deploy"}); isErr {
			t.Fatalf("get_skill: %s", text)
		}
	})
	if want := map[string]int64{"deploy|global|mcp": 1}; !reflect.DeepEqual(counts, want) {
		t.Errorf("goldie.skill.gets = %v, want %v", counts, want)
	}
}
