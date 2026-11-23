package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #457: goldie.syncs counts each finished poll of a
// Source, with the attributes source (the Source's name) and result, ok or
// error. app counts from the onPoll callback. The git Source's name is "git",
// as in its readiness check and its log records. The URL and path of the
// repository are never attributes (PC-12).

// Criterion: goldie.syncs counts each finished poll with source and result.
// One ok poll (the first load) and one failed poll (the repository is gone,
// the poll is triggered through POST /api/v1/sync) are each counted once. The
// export carries no repository URL or path.
func TestSourcePollsAreCountedByResult(t *testing.T) {
	shortPolls(t)
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
	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	cfg := gitConfig(t, repo.url, time.Hour) // only the first load and the triggered poll run
	cfg.GitAllowFileURL = true
	pub, err := PublicHandlerWith(cfg, nil, tel)
	if err != nil {
		t.Fatalf("PublicHandlerWith: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := platform.NewRegistry(0)
	startReturns(t, pub, ctx, reg)
	eventually(t, 5*time.Second, "the first load never served", func() bool {
		code, _ := pubGet(t, pub, "/api/v1/articles/git-first")
		return code == http.StatusOK
	})
	// The first load's callback runs just after the Snapshot is served. Poll
	// waits for that poll and its callback to end; it records no metric itself.
	_ = pub.sync.syncer.Poll(ctx)

	if err := os.RemoveAll(repo.dir); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, statusPath, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	pub.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST %s = %d %s, want 202", statusPath, rec.Code, rec.Body.String())
	}
	eventually(t, 5*time.Second, "the triggered poll never failed", func() bool {
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, statusPath, nil)
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		pub.ServeHTTP(w, r)
		return strings.Contains(w.Body.String(), `"last_error":"`)
	})
	_ = pub.sync.syncer.Poll(ctx) // the failed poll's callback runs after its status is set; Poll waits for it

	cancel()
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if err := tel.Shutdown(sctx); err != nil {
		t.Fatal(err)
	}

	raw := col.body("/v1/metrics")
	counts, keys := metricCounts(t, raw, "goldie.syncs", func(attrs map[string]string) string {
		return attrs["source"] + "/" + attrs["result"]
	})
	if counts["git/ok"] != 1 || counts["git/error"] != 1 || len(counts) != 2 {
		t.Errorf("goldie.syncs by source/result = %v, want git/ok 1 and git/error 1", counts)
	}
	if len(keys) != 2 || keys[0] != "result" || keys[1] != "source" {
		t.Errorf("goldie.syncs attribute keys = %v, want only result and source", keys)
	}
	for _, planted := range []string{repo.dir, repo.url, "git-first"} {
		if bytes.Contains(raw, []byte(planted)) {
			t.Errorf("the metrics export carries %q (PC-12)", planted)
		}
	}
}
