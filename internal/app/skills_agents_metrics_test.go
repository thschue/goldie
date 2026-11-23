package app

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// A get of a skill or an agent is not an article lookup: goldie.lookups
// ("Article lookups answered, by result") counts the one article lookup only.
func TestSkillAndAgentGetsAreNotCountedAsArticleLookups(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := loadedConfig(t, map[string]string{platform.EnvContentDir: testContent, platform.EnvDrainSeconds: "0"})
	addr, token, wait, err := Local{Config: cfg, Telemetry: tel}.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		"/api/v1/articles/platform%2Fdeploy": http.StatusOK,
		"/api/v1/skills/deploy":              http.StatusOK,
		"/api/v1/skills/nope":                http.StatusNotFound,
		"/api/v1/agents/reviewer":            http.StatusOK,
		"/api/v1/agents/nope":                http.StatusNotFound,
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test server
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("GET %s: status %d, want %d", path, resp.StatusCode, want)
		}
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the local server stopped with an error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the local server did not stop")
	}
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if err := tel.Shutdown(sctx); err != nil {
		t.Fatal(err)
	}
	counts, _ := metricCounts(t, col.body("/v1/metrics"), "goldie.lookups", func(attrs map[string]string) string { return attrs["result"] })
	if counts["hit"] != 1 || counts["miss"] != 0 || len(counts) != 1 {
		t.Errorf("goldie.lookups by result = %v, want the one article hit only", counts)
	}
}
