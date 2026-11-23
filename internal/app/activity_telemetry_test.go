package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #242 at the telemetry seam: the public handler as the
// server runs it, wrapped by Telemetry.WrapHandler with traces, logs and
// metrics exported over OTLP to an in-process collector, and the server
// logger writing PC-07 lines to a buffer and to the log bridge.

// otlpCollector keeps the raw body of every OTLP export, by path. OTLP over
// HTTP is protobuf and uncompressed by default, so strings and trace ids are
// in the bytes as they are.
type otlpCollector struct {
	mu     sync.Mutex
	bodies map[string][]byte
	srv    *httptest.Server
}

func newOTLPCollector(t *testing.T) *otlpCollector {
	t.Helper()
	c := &otlpCollector{bodies: map[string][]byte{}}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies[r.URL.Path] = append(c.bodies[r.URL.Path], b...)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *otlpCollector) body(path string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.bodies[path]...)
}

func (c *otlpCollector) all() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []byte
	for _, b := range c.bodies {
		out = append(out, b...)
	}
	return out
}

// tracedPublic is the public handler with the named tokens, wrapped and
// logged as cmd/goldie wires it with telemetry on. flush shuts telemetry
// down, which exports everything batched.
func tracedPublic(t *testing.T) (srv *httptest.Server, pub *Public, col *otlpCollector, logs *lockedBuffer, flush func()) {
	t.Helper()
	col = newOTLPCollector(t)
	env := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": col.srv.URL}
	tel, err := platform.SetupTelemetry(context.Background(), func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatalf("SetupTelemetry: %v", err)
	}
	logs = &lockedBuffer{}
	logger := tel.WrapLogger(platform.NewLogger(logs, slog.LevelDebug, "server"), "server")
	pub, err = PublicHandler(platform.Config{ContentDir: testContent, APITokens: namedTokenLines(), RateLimit: platform.DefaultRateLimit()}, logger)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv = httptest.NewServer(tel.WrapHandler(pub))
	t.Cleanup(srv.Close)
	flush = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tel.Shutdown(ctx); err != nil {
			t.Fatalf("telemetry shutdown: %v", err)
		}
	}
	return srv, pub, col, logs, flush
}

var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Criterion: each search and lookup is recorded with trace_id, the trace id
// of the request's server span (platform.TraceID), so an operator can go from
// an event to its trace.
func TestRecordedTraceIDIsTheRequestsSpan(t *testing.T) {
	srv, pub, col, _, flush := tracedPublic(t)
	for _, path := range []string{"/api/v1/search?q=kubernetes", "/api/v1/articles/no-such-article"} {
		resp, body := do(t, http.MethodGet, srv.URL+path, bearer(readerToken))
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s: status %d: %s", path, resp.StatusCode, body)
		}
	}
	events := waitEvents(t, pub.Activity(), 2)
	flush()
	spans := col.body("/v1/traces")
	if len(spans) == 0 {
		t.Fatal("fixture: no spans were exported")
	}
	seen := map[string]bool{}
	for _, e := range events {
		if !traceIDPattern.MatchString(e.TraceID) || e.TraceID == strings.Repeat("0", 32) {
			t.Errorf("%s %q: trace id %q, want 32 lowercase hex digits, not all zero", e.Kind, e.Query, e.TraceID)
			continue
		}
		if seen[e.TraceID] {
			t.Errorf("two requests recorded the same trace id %s", e.TraceID)
		}
		seen[e.TraceID] = true
		raw, _ := hex.DecodeString(e.TraceID)
		if !bytes.Contains(spans, raw) {
			t.Errorf("%s %q: trace id %s is not the id of any exported span", e.Kind, e.Query, e.TraceID)
		}
	}
}

// Criterion: query text and ids never become log fields, metric labels or
// span attributes. Planted search words, article ids and Project ids go
// through the API and /mcp, served and rejected, and none of them reaches a
// log line or anything exported.
func TestQueryTextAndIDsNeverReachLogsSpansOrMetrics(t *testing.T) {
	srv, pub, col, logs, flush := tracedPublic(t)
	for path, status := range map[string]int{
		"/api/v1/search?q=plantedwords&project=github.com/acme/plantedproj": http.StatusOK,
		"/api/v1/search?q=kubernetes%20plantedmore":                         http.StatusOK,
		"/api/v1/articles/plantedid":                                        http.StatusNotFound,
		"/api/v1/articles/platform%2Fplantednested":                         http.StatusNotFound,
		"/api/v1/articles/platform%2Fdeploy?project=github.com/acme/shop":   http.StatusOK,
		"/api/v1/search?q=planted" + strings.Repeat("a", 1025):              http.StatusBadRequest,
	} {
		if resp, body := do(t, http.MethodGet, srv.URL+path, bearer(readerToken)); resp.StatusCode != status {
			t.Fatalf("fixture: GET %s: status %d, want %d: %s", path, resp.StatusCode, status, body)
		}
	}
	if resp, _ := do(t, http.MethodGet, srv.URL+"/api/v1/articles/plantedanon", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("fixture: anonymous lookup: status %d, want 401", resp.StatusCode)
	}
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(readerToken, http.DefaultTransport))
	call(t, cs, "search_knowledge", map[string]any{"query": "plantedmcpwords"})
	call(t, cs, "get_article", map[string]any{"id": "plantedmcpid"})
	events := waitEvents(t, pub.Activity(), 7) // the five served requests and the two tool calls
	flush()

	// The planted text did reach the server, and the export is readable.
	queries := map[string]bool{}
	for _, e := range events {
		queries[e.Query] = true
	}
	if !queries["plantedwords"] || !queries["plantedid"] || !queries["plantedmcpwords"] {
		t.Fatalf("fixture: the planted searches and lookups are not recorded: %+v", events)
	}
	raw, _ := hex.DecodeString(events[0].TraceID)
	if len(raw) != 16 || !bytes.Contains(col.body("/v1/traces"), raw) {
		t.Fatalf("fixture: the exported spans are not readable as raw bytes (trace id %q)", events[0].TraceID)
	}

	leaks := []string{"planted", "platform/deploy", "platform%2Fdeploy", "platform/planted", "github.com/acme/shop", readerToken}
	logText := strings.Join(logs.lines(), "\n")
	exported := col.all()
	for _, leak := range leaks {
		if strings.Contains(logText, leak) {
			t.Errorf("a log line carries %q:\n%s", leak, logText)
		}
		if i := bytes.Index(exported, []byte(leak)); i >= 0 {
			from, to := max(i-60, 0), min(i+len(leak)+20, len(exported))
			t.Errorf("an exported span, log or metric carries %q: ...%q...", leak, exported[from:to])
		}
	}
}
