package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// skillGetCounts serves the skill content with metrics export on, runs drive
// against it, and returns goldie.skill.gets as name|scope|via counts.
func skillGetCounts(t *testing.T, drive func(srvURL string)) map[string]int64 {
	t.Helper()
	return skillGetCountsWith(t, platform.Config{}, drive)
}

// skillGetCountsWith is skillGetCounts over cfg, with the content, token and
// rate limit filled in.
func skillGetCountsWith(t *testing.T, cfg platform.Config, drive func(srvURL string)) map[string]int64 {
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
	cfg.ContentDir, cfg.APIToken, cfg.RateLimit = skillGetsContent(t), testToken, roomyRateLimit
	pub, err := PublicHandlerWith(cfg, nil, tel)
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

// X-Goldie-Via is the client's own word: only exactly one value of mcp makes
// a get mcp. Any other value, a repeated header or a huge one leaves what
// X-Goldie-Client alone gives, and the version gate ignores it.
func TestHostileViaHeadersLeaveTheClientHeaderToDecide(t *testing.T) {
	via := func(client string, values ...string) func(*http.Request) {
		return func(r *http.Request) {
			bearer(testToken)(r)
			if client != "" {
				r.Header.Set(clientHeader, client)
			}
			for _, v := range values {
				r.Header.Add("X-Goldie-Via", v)
			}
		}
	}
	counts := skillGetCountsWith(t, platform.Config{MinClientVersion: semver(t, minVersion)}, func(url string) {
		get := func(status int, mutate func(*http.Request)) {
			t.Helper()
			if resp, body := do(t, http.MethodGet, url+"/api/v1/skills/deploy", mutate); resp.StatusCode != status {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, status, body)
			}
		}
		ok := http.StatusOK
		get(ok, via("goldie/"+minVersion, "MCP"))
		get(ok, via("goldie/"+minVersion, "mcp, cli"))
		get(ok, via("goldie/"+minVersion, strings.Repeat("m", 4096)))
		get(ok, via("goldie/"+minVersion, "mcp", "mcp"))
		get(ok, via("curl/8.5.0", "MCP"))
		get(ok, via("", "mcp, cli"))
		// Exactly mcp is mcp, whatever the client header says.
		get(ok, via("goldie/"+minVersion, "mcp"))
		// An old client claiming mcp is still refused, and not counted.
		get(http.StatusUpgradeRequired, via("goldie/0.0.1", "mcp"))
	})
	want := map[string]int64{"deploy|global|cli": 4, "deploy|global|other": 2, "deploy|global|mcp": 1}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("goldie.skill.gets = %v, want %v", counts, want)
	}
}
