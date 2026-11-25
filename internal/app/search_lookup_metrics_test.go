package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #456 at the HTTP seam: a local server (as goldie
// server --dev runs it) with Telemetry exporting metrics over OTLP to an
// in-process collector. goldie.searches and goldie.lookups count each
// answered request with one attribute, result, hit or miss. Query text,
// article ids and Project ids are never attributes (PC-12).

// pbFields calls f with each field of the protobuf message b: its number and
// value (a varint or fixed number as uint64, or the bytes of a length-delimited
// field). The OTLP messages are decoded by hand because the generated types
// belong to internal/platform (ADR 0017).
func pbFields(t *testing.T, b []byte, f func(num protowire.Number, n uint64, raw []byte)) {
	t.Helper()
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			t.Fatalf("decode the metrics export: %v", protowire.ParseError(n))
		}
		b = b[n:]
		var (
			v   uint64
			raw []byte
		)
		switch typ {
		case protowire.VarintType:
			v, n = protowire.ConsumeVarint(b)
		case protowire.Fixed64Type:
			v, n = protowire.ConsumeFixed64(b)
		case protowire.Fixed32Type:
			var v32 uint32
			v32, n = protowire.ConsumeFixed32(b)
			v = uint64(v32)
		case protowire.BytesType:
			raw, n = protowire.ConsumeBytes(b)
		default:
			t.Fatalf("decode the metrics export: unexpected wire type %v", typ)
		}
		if n < 0 {
			t.Fatalf("decode the metrics export: %v", protowire.ParseError(n))
		}
		b = b[n:]
		f(num, v, raw)
	}
}

// metricCounts is the sum per point key of the data points of the metric called
// name in an OTLP metrics export, and every attribute key seen on it. pointKey
// derives a point's key from its string attributes.
func metricCounts(t *testing.T, export []byte, name string, pointKey func(attrs map[string]string) string) (counts map[string]int64, keys []string) {
	t.Helper()
	counts = map[string]int64{}
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
		counts[pointKey(attrs)] += value
	}
	metric := func(b []byte) {
		var isNamed bool
		pbFields(t, b, func(num protowire.Number, _ uint64, raw []byte) {
			switch num {
			case 1:
				isNamed = string(raw) == name
			case 7: // sum
				if isNamed {
					pbFields(t, raw, func(num protowire.Number, _ uint64, raw []byte) {
						if num == 1 {
							point(raw)
						}
					})
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
	sort.Strings(keys)
	return counts, keys
}

// Criterion: goldie.searches and goldie.lookups count each answered request
// with one attribute, result, hit or miss. A request a guard rejects is not
// counted, and no query text, article id or Project id is exported.
func TestSearchesAndLookupsAreCountedByHitOrMiss(t *testing.T) {
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
	get := func(path string, authed bool) int {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if authed {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback test server
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	const (
		missQuery = "planted-query-marker-4471"
		missID    = "planted-id-marker-9082"
		project   = "github.com/acme/planted-project-marker-3317"
	)
	for _, c := range []struct {
		path   string
		authed bool
		status int
	}{
		{"/api/v1/search?q=kubernetes", true, http.StatusOK},
		{"/api/v1/search?q=" + missQuery + "&project=" + url.QueryEscape(project), true, http.StatusOK},
		{"/api/v1/articles/platform%2Fdeploy", true, http.StatusOK},
		{"/api/v1/articles/" + missID, true, http.StatusNotFound},
		// Rejected before the Snapshot answered: not counted.
		{"/api/v1/search?q=kubernetes", false, http.StatusUnauthorized},
		{"/api/v1/articles/platform%2Fdeploy", false, http.StatusUnauthorized},
	} {
		if got := get(c.path, c.authed); got != c.status {
			t.Fatalf("GET %s: status %d, want %d", c.path, got, c.status)
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

	raw := col.body("/v1/metrics")
	var attrValues []string
	for _, name := range []string{"goldie.searches", "goldie.lookups"} {
		counts, keys := metricCounts(t, raw, name, func(attrs map[string]string) string {
			for _, v := range attrs {
				attrValues = append(attrValues, v)
			}
			return attrs["result"]
		})
		if counts["hit"] != 1 || counts["miss"] != 1 || len(counts) != 2 {
			t.Errorf("%s by result = %v, want one hit and one miss", name, counts)
		}
		if len(keys) != 1 || keys[0] != "result" {
			t.Errorf("%s attribute keys = %v, want only result", name, keys)
		}
	}
	for _, planted := range []string{missQuery, missID, "planted-project-marker-3317"} {
		if bytes.Contains(raw, []byte(planted)) {
			t.Errorf("the metrics export carries %q (PC-12)", planted)
		}
	}
	// goldie.item.uses carries the Item id by design (#898), so the id of the
	// looked-up article is checked on the searches and lookups points only.
	for _, v := range attrValues {
		if strings.Contains(v, "platform/deploy") {
			t.Errorf("goldie.searches or goldie.lookups carries the Item id in attribute value %q (PC-12)", v)
		}
	}
}
