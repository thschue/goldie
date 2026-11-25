package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/pem"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

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
	var syncAttrValues []string
	counts, keys := metricCounts(t, raw, "goldie.syncs", func(attrs map[string]string) string {
		for _, v := range attrs {
			syncAttrValues = append(syncAttrValues, v)
		}
		return attrs["source"] + "/" + attrs["result"]
	})
	if counts["git/ok"] != 1 || counts["git/error"] != 1 || len(counts) != 2 {
		t.Errorf("goldie.syncs by source/result = %v, want git/ok 1 and git/error 1", counts)
	}
	if len(keys) != 2 || keys[0] != "result" || keys[1] != "source" {
		t.Errorf("goldie.syncs attribute keys = %v, want only result and source", keys)
	}
	for _, planted := range []string{repo.dir, repo.url} {
		if bytes.Contains(raw, []byte(planted)) {
			t.Errorf("the metrics export carries %q (PC-12)", planted)
		}
	}
	// goldie.item.uses carries the Item id by design (#898), so the id of the
	// fetched article is checked on the goldie.syncs points only.
	for _, v := range syncAttrValues {
		if strings.Contains(v, "git-first") {
			t.Errorf("goldie.syncs carries the Item id in attribute value %q (PC-12)", v)
		}
	}
}

// Acceptance tests for #604: goldie.sync.duration records how long each
// finished poll took, in seconds, as a histogram with the attributes of
// goldie.syncs (source and result) and nothing else (PC-12).

// histPoint is one data point of a histogram in an OTLP metrics export.
type histPoint struct {
	attrs  map[string]string
	count  uint64
	sum    float64
	bounds []float64
}

// histogramPoints is the unit and the data points of the histogram called
// name in an OTLP metrics export. found is false when the export has no
// histogram of that name.
func histogramPoints(t *testing.T, export []byte, name string) (unit string, points []histPoint, found bool) {
	t.Helper()
	point := func(b []byte) histPoint {
		hp := histPoint{attrs: map[string]string{}}
		pbFields(t, b, func(num protowire.Number, n uint64, raw []byte) {
			switch num {
			case 4:
				hp.count = n
			case 5:
				hp.sum = math.Float64frombits(n)
			case 7:
				for ; len(raw) >= 8; raw = raw[8:] {
					hp.bounds = append(hp.bounds, math.Float64frombits(binary.LittleEndian.Uint64(raw)))
				}
			case 9:
				var key, str string
				pbFields(t, raw, func(num protowire.Number, _ uint64, raw []byte) {
					switch num {
					case 1:
						key = string(raw)
					case 2:
						pbFields(t, raw, func(num protowire.Number, _ uint64, raw []byte) {
							if num == 1 {
								str = string(raw)
							}
						})
					}
				})
				hp.attrs[key] = str
			}
		})
		return hp
	}
	metric := func(b []byte) {
		var isNamed bool
		var u string
		var pts []histPoint
		pbFields(t, b, func(num protowire.Number, _ uint64, raw []byte) {
			switch num {
			case 1:
				isNamed = string(raw) == name
			case 3:
				u = string(raw)
			case 9: // histogram
				pbFields(t, raw, func(num protowire.Number, _ uint64, raw []byte) {
					if num == 1 {
						pts = append(pts, point(raw))
					}
				})
			}
		})
		if isNamed && pts != nil {
			unit, points, found = u, append(points, pts...), true
		}
	}
	pbFields(t, export, func(num protowire.Number, _ uint64, resourceMetrics []byte) {
		if num != 1 {
			return
		}
		pbFields(t, resourceMetrics, func(num protowire.Number, _ uint64, scopeMetrics []byte) {
			if num != 2 {
				return
			}
			pbFields(t, scopeMetrics, func(num protowire.Number, _ uint64, m []byte) {
				if num == 2 {
					metric(m)
				}
			})
		})
	})
	return unit, points, found
}

// metricsTelemetry is Telemetry exporting metrics to col with the given
// OTEL_METRICS_EXPORTER.
func metricsTelemetry(t *testing.T, col *otlpCollector, metricsExporter string) *platform.Telemetry {
	t.Helper()
	env := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": col.srv.URL,
		"OTEL_TRACES_EXPORTER":        "none",
		"OTEL_LOGS_EXPORTER":          "none",
		"OTEL_METRICS_EXPORTER":       metricsExporter,
	}
	tel, err := platform.SetupTelemetry(context.Background(), func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatalf("SetupTelemetry: %v", err)
	}
	return tel
}

func flushTelemetry(t *testing.T, tel *platform.Telemetry) {
	t.Helper()
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if err := tel.Shutdown(sctx); err != nil {
		t.Fatal(err)
	}
}

// Criterion: goldie.sync.duration is a Float64 histogram with unit s and the
// attributes source and result, with the same sets as goldie.syncs. A failed
// poll records its duration with result=error. The bucket boundaries are
// explicit, from about 0.05 s to 60 s. No URL, path or error text is exported.
func TestSourcePollDurationIsRecordedByResult(t *testing.T) {
	shortPolls(t)
	col := newOTLPCollector(t)
	tel := metricsTelemetry(t, col, "otlp")
	if !tel.Enabled() {
		t.Fatal("telemetry is not enabled")
	}
	repo := newBareRepo(t)
	repo.commit(gitArticle("git-first"))
	cfg := gitConfig(t, repo.url, time.Hour)
	cfg.GitAllowFileURL = true
	pub, err := PublicHandlerWith(cfg, nil, tel)
	if err != nil {
		t.Fatalf("PublicHandlerWith: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startReturns(t, pub, ctx, platform.NewRegistry(0))
	eventually(t, 5*time.Second, "the first load never served", func() bool {
		code, _ := pubGet(t, pub, "/api/v1/articles/git-first")
		return code == http.StatusOK
	})
	_ = pub.sync.syncer.Poll(ctx) // waits for the first poll's callback

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
	_ = pub.sync.syncer.Poll(ctx) // waits for the failed poll's callback

	cancel()
	flushTelemetry(t, tel)

	raw := col.body("/v1/metrics")
	unit, points, found := histogramPoints(t, raw, "goldie.sync.duration")
	if !found {
		t.Fatal("the export has no goldie.sync.duration histogram")
	}
	if unit != "s" {
		t.Errorf("goldie.sync.duration unit = %q, want s", unit)
	}
	byResult := map[string]histPoint{}
	for _, p := range points {
		if len(p.attrs) != 2 {
			t.Errorf("goldie.sync.duration point attributes = %v, want only source and result", p.attrs)
		}
		if p.attrs["source"] != "git" {
			t.Errorf("goldie.sync.duration source = %q, want git", p.attrs["source"])
		}
		r := p.attrs["result"]
		if _, dup := byResult[r]; dup {
			t.Errorf("two goldie.sync.duration points for result %q", r)
		}
		byResult[r] = p
	}
	if len(byResult) != 2 {
		t.Fatalf("goldie.sync.duration results = %v, want ok and error", byResult)
	}
	for _, r := range []string{"ok", "error"} {
		p, has := byResult[r]
		if !has {
			t.Errorf("no goldie.sync.duration point with result=%s", r)
			continue
		}
		if p.count != 1 {
			t.Errorf("goldie.sync.duration result=%s count = %d, want 1", r, p.count)
		}
		if p.sum < 0 || p.sum >= 60 {
			t.Errorf("goldie.sync.duration result=%s sum = %v s, want a local poll's seconds in [0, 60)", r, p.sum)
		}
		checkPollBuckets(t, p.bounds)
	}
	for _, planted := range []string{repo.dir, repo.url} {
		if bytes.Contains(raw, []byte(planted)) {
			t.Errorf("the metrics export carries %q (PC-12)", planted)
		}
	}
	// goldie.item.uses carries the Item id by design (#898), so the id of the
	// fetched article is checked on the goldie.sync.duration points only.
	for _, p := range points {
		for _, v := range p.attrs {
			if strings.Contains(v, "git-first") {
				t.Errorf("goldie.sync.duration carries the Item id in attribute value %q (PC-12)", v)
			}
		}
	}
}

// checkPollBuckets fails unless bounds are explicit seconds from about 0.05 to
// 60, not the SDK default (0, 5, 10, ... 10000, in milliseconds).
func checkPollBuckets(t *testing.T, bounds []float64) {
	t.Helper()
	if len(bounds) < 5 || len(bounds) > 20 {
		t.Errorf("bucket bounds = %v, want 5 to 20 explicit bounds", bounds)
		return
	}
	if !sort.Float64sAreSorted(bounds) {
		t.Errorf("bucket bounds %v are not ascending", bounds)
	}
	if first := bounds[0]; first < 0.01 || first > 0.1 {
		t.Errorf("first bucket bound = %v, want about 0.05 s", first)
	}
	if last := bounds[len(bounds)-1]; last < 30 || last > 120 {
		t.Errorf("last bucket bound = %v, want about 60 s", last)
	}
}

// Criterion: a poll cut short by shutdown records nothing. The git remote
// accepts the request and never answers, so the first poll is in flight when
// the server stops; neither goldie.syncs nor goldie.sync.duration gets a point.
func TestPollCutShortByShutdownRecordsNoDuration(t *testing.T) {
	shortPolls(t)
	var hit atomic.Bool
	release := make(chan struct{})
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(remote.Close)
	t.Cleanup(func() { close(release) })

	col := newOTLPCollector(t)
	tel := metricsTelemetry(t, col, "otlp")
	cfg := gitConfig(t, remote.URL+"/repo.git", time.Hour)
	cfg.GitCABundle = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: remote.Certificate().Raw}))
	pub, err := PublicHandlerWith(cfg, nil, tel)
	if err != nil {
		t.Fatalf("PublicHandlerWith: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startReturns(t, pub, ctx, platform.NewRegistry(0))
	eventually(t, 5*time.Second, "the poll never reached the remote", hit.Load)

	cancel()
	// Poll waits for the in-flight poll and its callback to end. Its own
	// context is already cancelled, so it loads nothing and records nothing.
	_ = pub.sync.syncer.Poll(ctx)
	flushTelemetry(t, tel)

	raw := col.body("/v1/metrics")
	if _, points, found := histogramPoints(t, raw, "goldie.sync.duration"); found || len(points) != 0 {
		t.Errorf("goldie.sync.duration has points %v for a poll cut short by shutdown, want none", points)
	}
	if counts, _ := metricCounts(t, raw, "goldie.syncs", func(a map[string]string) string { return a["result"] }); len(counts) != 0 {
		t.Errorf("goldie.syncs = %v for a poll cut short by shutdown, want none", counts)
	}
}

// Criterion: with metrics off, or a nil Telemetry, nothing is recorded and
// nothing panics.
func TestPollDurationWithMetricsOffOrNoTelemetryRecordsNothing(t *testing.T) {
	shortPolls(t)
	for name, mk := range map[string]func(t *testing.T, col *otlpCollector) *platform.Telemetry{
		"nil telemetry": func(*testing.T, *otlpCollector) *platform.Telemetry { return nil },
		"metrics none":  func(t *testing.T, col *otlpCollector) *platform.Telemetry { return metricsTelemetry(t, col, "none") },
	} {
		t.Run(name, func(t *testing.T) {
			col := newOTLPCollector(t)
			tel := mk(t, col)
			repo := newBareRepo(t)
			repo.commit(gitArticle("git-first"))
			cfg := gitConfig(t, repo.url, time.Hour)
			cfg.GitAllowFileURL = true
			pub, err := PublicHandlerWith(cfg, nil, tel)
			if err != nil {
				t.Fatalf("PublicHandlerWith: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			startReturns(t, pub, ctx, platform.NewRegistry(0))
			eventually(t, 5*time.Second, "the first load never served", func() bool {
				code, _ := pubGet(t, pub, "/api/v1/articles/git-first")
				return code == http.StatusOK
			})
			_ = pub.sync.syncer.Poll(ctx)
			cancel()
			if tel != nil {
				flushTelemetry(t, tel)
			}
			if raw := col.body("/v1/metrics"); len(raw) != 0 {
				if _, _, found := histogramPoints(t, raw, "goldie.sync.duration"); found {
					t.Error("goldie.sync.duration was exported with metrics off")
				}
			}
		})
	}
}
