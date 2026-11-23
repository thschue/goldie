package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #595: the gauge goldie.items counts the Items of the
// current Snapshot by scope (global, project, local) and kind (article, agent,
// instruction, skill), read at each collection. app connects it to
// Syncer.Current(). The git Source serves the content, with metrics exported
// over OTLP to an in-process collector every 100 ms. No Project id, Source
// name, URL or path is an attribute (PC-12).

const (
	gaugeAlpha = "github.com/acme/alpha"
	gaugeBeta  = "github.com/acme/beta"
)

// exportLog keeps each OTLP metrics export as its own body, so a gauge read at
// one collection is not merged with the next.
type exportLog struct {
	mu   sync.Mutex
	srv  *httptest.Server
	list [][]byte
}

func newExportLog(t *testing.T) *exportLog {
	t.Helper()
	l := &exportLog{}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/metrics" {
			l.mu.Lock()
			l.list = append(l.list, b)
			l.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *exportLog) exports() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.list)
}

// gaugeSeries are the data points of goldie.items in one export, by
// "scope/kind", and the attribute keys seen. series is empty when the export
// has no goldie.items data point.
func gaugeSeries(t *testing.T, export []byte) (series map[string]int64, keys []string) {
	t.Helper()
	series = map[string]int64{}
	seen := map[string]bool{}
	point := func(b []byte) {
		attrs := map[string]string{}
		var value int64
		pbFields(t, b, func(num protowire.Number, n uint64, raw []byte) {
			switch num {
			case 6: // as_int
				value = int64(n) //nolint:gosec // a small count
			case 7: // attributes
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
				seen[key] = true
				attrs[key] = str
			}
		})
		series[attrs["scope"]+"/"+attrs["kind"]] = value
	}
	metric := func(b []byte) {
		var isNamed bool
		pbFields(t, b, func(num protowire.Number, _ uint64, raw []byte) {
			switch num {
			case 1:
				isNamed = string(raw) == "goldie.items"
			case 5: // gauge
				if isNamed {
					pbFields(t, raw, func(num protowire.Number, _ uint64, raw []byte) {
						if num == 1 {
							point(raw)
						}
					})
				}
			case 7: // sum: goldie.items is a gauge
				if isNamed {
					t.Error("goldie.items is exported as a sum, want a gauge")
				}
			}
		})
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
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return series, keys
}

// gaugeServer starts the public handler over a git Source at repo, exporting
// metrics every 100 ms. Its content is served once the first load is done.
func gaugeServer(t *testing.T, repo *bareRepo, log *exportLog) (*Public, context.Context) {
	t.Helper()
	shortPolls(t)
	env := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": log.srv.URL,
		"OTEL_TRACES_EXPORTER":        "none",
		"OTEL_LOGS_EXPORTER":          "none",
		"OTEL_METRICS_EXPORTER":       "otlp",
		"OTEL_METRIC_EXPORT_INTERVAL": "100",
	}
	tel, err := platform.SetupTelemetry(context.Background(), func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	cfg := gitConfig(t, repo.url, time.Hour)
	cfg.GitAllowFileURL = true
	pub, err := PublicHandlerWith(cfg, nil, tel)
	if err != nil {
		t.Fatalf("PublicHandlerWith: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer scancel()
		_ = tel.Shutdown(sctx)
	})
	startReturns(t, pub, ctx, platform.NewRegistry(0))
	return pub, ctx
}

func gaugeFiles(extra map[string]string) map[string]string {
	files := map[string]string{
		"global/articles/a.md":                         "---\nname: a\nkeywords: [x]\n---\nA.\n",
		"global/articles/b.md":                         "---\nname: b\nkeywords: [x]\n---\nB.\n",
		"global/agents/bot.md":                         "---\nname: bot\ndescription: d\n---\nYou are bot.\n",
		"projects/" + gaugeAlpha + "/articles/p.md":    "---\nname: p\nkeywords: [x]\n---\nP.\n",
		"projects/" + gaugeBeta + "/articles/q.md":     "---\nname: q\nkeywords: [x]\n---\nQ.\n",
		"projects/" + gaugeBeta + "/skills/s/SKILL.md": "# S\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

// latestGauge is the goldie.items series of the newest export that has any.
func latestGauge(t *testing.T, log *exportLog) (map[string]int64, []string) {
	t.Helper()
	exports := log.exports()
	for i := len(exports) - 1; i >= 0; i-- {
		if s, k := gaugeSeries(t, exports[i]); len(s) > 0 {
			return s, k
		}
	}
	return nil, nil
}

func wantGaugeSeries(t *testing.T, got map[string]int64, nonZero map[string]int64) {
	t.Helper()
	scopes := []string{"global", "project", "local"}
	kinds := []string{"article", "agent", "loop", "instruction", "skill"}
	if want := len(scopes) * len(kinds); len(got) != want {
		t.Errorf("goldie.items has %d series %v, want all %d", len(got), got, want)
	}
	for _, scope := range scopes {
		for _, kind := range kinds {
			key := scope + "/" + kind
			if v, ok := got[key]; !ok || v != nonZero[key] {
				t.Errorf("goldie.items %s = %d (present %v), want %d", key, v, ok, nonZero[key])
			}
		}
	}
}

// Criterion: goldie.items counts the served Items by scope and kind. Two
// Projects sum into one project series, empty series are 0, only scope and kind
// are attributes, and no Project id, Source name, URL or path is exported.
func TestItemsGaugeCountsTheSnapshotByScopeAndKind(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gaugeFiles(nil))
	log := newExportLog(t)
	gaugeServer(t, repo, log)

	eventually(t, 10*time.Second, "goldie.items never carried the first Snapshot's counts", func() bool {
		s, _ := latestGauge(t, log)
		return s["global/article"] == 2
	})
	got, keys := latestGauge(t, log)
	wantGaugeSeries(t, got, map[string]int64{
		"global/article": 2, "global/agent": 1,
		"project/article": 2, "project/skill": 1,
	})
	if !slices.Equal(keys, []string{"kind", "scope"}) {
		t.Errorf("goldie.items attribute keys = %v, want only kind and scope", keys)
	}
	for _, export := range log.exports() {
		for _, planted := range []string{gaugeAlpha, gaugeBeta, "acme", repo.dir, repo.url, `"git"`} {
			if bytes.Contains(export, []byte(planted)) {
				t.Errorf("the metrics export carries %q (PC-12)", planted)
			}
		}
	}
}

// Criterion: after a sync, the next collection shows the new counts.
func TestItemsGaugeFollowsASync(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gaugeFiles(nil))
	log := newExportLog(t)
	pub, ctx := gaugeServer(t, repo, log)
	eventually(t, 10*time.Second, "goldie.items never carried the first Snapshot's counts", func() bool {
		s, _ := latestGauge(t, log)
		return s["global/article"] == 2
	})

	// Add a global article and an instruction, drop Project beta.
	next := gaugeFiles(map[string]string{
		"global/articles/c.md":        "---\nname: c\nkeywords: [x]\n---\nC.\n",
		"global/instructions/rule.md": "---\nname: rule\norder: 1\ntargets: [claude]\n---\nBody.\n",
	})
	delete(next, "projects/"+gaugeBeta+"/articles/q.md")
	delete(next, "projects/"+gaugeBeta+"/skills/s/SKILL.md")
	repo.commit(next)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, statusPath, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	pub.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST %s = %d %s, want 202", statusPath, rec.Code, rec.Body.String())
	}
	eventually(t, 10*time.Second, "goldie.items never followed the sync", func() bool {
		s, _ := latestGauge(t, log)
		return s["global/article"] == 3
	})
	got, _ := latestGauge(t, log)
	wantGaugeSeries(t, got, map[string]int64{
		"global/article": 3, "global/agent": 1, "global/instruction": 1,
		"project/article": 1,
	})
}

// Criterion: before the first Snapshot there are no data points. The
// repository is gone, so no load ever succeeds, yet metrics are exported.
func TestItemsGaugeHasNoPointsBeforeTheFirstSnapshot(t *testing.T) {
	repo := newBareRepo(t)
	repo.commit(gaugeFiles(nil))
	if err := os.RemoveAll(repo.dir); err != nil {
		t.Fatal(err)
	}
	log := newExportLog(t)
	gaugeServer(t, repo, log)

	// goldie.syncs counts the failed first poll, so exports exist.
	eventually(t, 10*time.Second, "no metrics export arrived", func() bool { return len(log.exports()) >= 3 })
	for _, export := range log.exports() {
		if s, _ := gaugeSeries(t, export); len(s) != 0 {
			t.Fatalf("goldie.items has data points %v before any Snapshot", s)
		}
	}
}
