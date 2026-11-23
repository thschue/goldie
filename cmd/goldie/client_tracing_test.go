package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #281 at the CLI boundary: the real binary, run as
// `goldie lookup` and as `goldie mcp --transport stdio`, against a fake API
// that records the headers it receives and a fake OTLP collector. They use
// only net/http and the binary: no OpenTelemetry type leaves
// internal/platform (#13). They rely on goldieBinary (server_test.go),
// mcpCommand and syncBuffer (mcp_stdio_test.go),
// closedServerAddr (cli_version_addr_test.go) and cliToken (cli_test.go).

// Client roles must finish within this, whatever the collector does: the
// flush is bounded at about 2s, the rest is slack for a loaded machine.
const clientExitBound = 5 * time.Second

// sdkNotice is the fixed text goldie logs for whatever the OpenTelemetry SDK
// reports.
const sdkNotice = "OpenTelemetry SDK reported"

// The article the clients fetch and a Project that goes out as a query
// parameter. Neither may reach the collector.
const (
	tracedArticle = "platform/deploy"
	tracedProject = "github.com/planted-org/planted-repo-6b3e"
)

var traceparentRE = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-0[0-9a-f]$`)

// headerAPI is the real in-process server over the CLI test content,
// recording the headers of every request.
type headerAPI struct {
	srv     *httptest.Server
	mu      sync.Mutex
	headers []http.Header
}

func newHeaderAPI(t *testing.T) *headerAPI {
	t.Helper()
	h, err := app.PublicHandler(platform.Config{ContentDir: "../../internal/app/testdata/content", APIToken: cliToken, RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	a := &headerAPI{}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.headers = append(a.headers, r.Header.Clone())
		a.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *headerAPI) seen() []http.Header {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]http.Header(nil), a.headers...)
}

// otlpCollector counts the export requests per path and keeps their bodies.
// It answers every request with status.
type otlpCollector struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   map[string]int
	bodies map[string][]byte
}

func newOTLPCollector(t *testing.T, status int) *otlpCollector {
	t.Helper()
	c := &otlpCollector{hits: map[string]int{}, bodies: map[string][]byte{}}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			if zr, err := gzip.NewReader(r.Body); err == nil {
				body = zr
			}
		}
		b, _ := io.ReadAll(body)
		c.mu.Lock()
		c.hits[r.URL.Path]++
		c.bodies[r.URL.Path] = append(c.bodies[r.URL.Path], b...)
		c.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *otlpCollector) snapshot() (map[string]int, map[string][]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hits, bodies := map[string]int{}, map[string][]byte{}
	for k, v := range c.hits {
		hits[k] = v
	}
	for k, v := range c.bodies {
		bodies[k] = append([]byte(nil), v...)
	}
	return hits, bodies
}

// hangingCollector accepts export requests and never answers them until the
// client goes away or the test ends.
func hangingCollector(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close
	return srv.URL
}

// runClientBinary runs the binary with args in an empty directory, with only
// PATH, an empty HOME and env as its environment.
func runClientBinary(t *testing.T, env []string, args ...string) (stdout, stderr string, took time.Duration, err error) {
	t.Helper()
	bin := goldieBinary(t) // build before the clock starts
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, env...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	start := time.Now()
	err = cmd.Run()
	return out.String(), errOut.String(), time.Since(start), err
}

func lookupArgs(api string) []string {
	return []string{"lookup", tracedArticle, "--addr", api, "--token", cliToken, "--project", tracedProject}
}

// baselineLookup is the output of lookup with no telemetry configured.
func baselineLookup(t *testing.T, api string) string {
	t.Helper()
	out, stderr, _, err := runClientBinary(t, nil, lookupArgs(api)...)
	if err != nil || !strings.Contains(out, "Deploying to Kubernetes") {
		t.Fatalf("lookup without telemetry: err %v, stdout:\n%s\nstderr:\n%s", err, out, stderr)
	}
	return out
}

// traceIDs returns the trace id of every API request's traceparent and fails
// the test unless each carries a valid one and none carries baggage.
func traceIDs(t *testing.T, headers []http.Header) [][]byte {
	t.Helper()
	if len(headers) == 0 {
		t.Fatal("the API saw no request")
	}
	var ids [][]byte
	for i, h := range headers {
		m := traceparentRE.FindStringSubmatch(h.Get("Traceparent"))
		if m == nil {
			t.Errorf("request %d: traceparent %q, want a W3C traceparent", i, h.Get("Traceparent"))
			continue
		}
		id, _ := hex.DecodeString(m[1])
		ids = append(ids, id)
		if v, ok := h["Baggage"]; ok {
			t.Errorf("request %d carries baggage %q; propagation is TraceContext only", i, v)
		}
	}
	return ids
}

// assertNoTraceparent fails the test if any API request carried trace context.
func assertNoTraceparent(t *testing.T, headers []http.Header) {
	t.Helper()
	if len(headers) == 0 {
		t.Fatal("the API saw no request")
	}
	for i, h := range headers {
		for _, name := range []string{"Traceparent", "Tracestate", "Baggage"} {
			if v, ok := h[name]; ok {
				t.Errorf("request %d carries %s %q with client tracing off", i, name, v)
			}
		}
	}
}

// assertClientExport checks what the collector got from one client run:
// traces only, carrying goldie.mode=mode and the trace ids the API saw, and
// nothing of the token, the article id or the Project.
func assertClientExport(t *testing.T, c *otlpCollector, mode string, ids [][]byte) {
	t.Helper()
	hits, bodies := c.snapshot()
	for path, n := range hits {
		if path != "/v1/traces" {
			t.Errorf("the collector got %d requests on %s; client roles export traces only", n, path)
		}
	}
	body := bodies["/v1/traces"]
	if len(body) == 0 {
		t.Fatalf("the collector got no traces (hits: %v)", hits)
	}
	i := bytes.Index(body, []byte("goldie.mode"))
	if i < 0 {
		t.Error("the export carries no goldie.mode resource attribute")
	} else if rest := body[i+len("goldie.mode"):]; !bytes.Contains(rest[:min(len(rest), 16)], []byte(mode)) {
		t.Errorf("goldie.mode is not %q", mode)
	}
	for _, id := range ids {
		if !bytes.Contains(body, id) {
			t.Errorf("the export lacks the trace %x the API request carried", id)
		}
	}
	for _, bad := range []string{cliToken, "Bearer", tracedArticle, strings.ReplaceAll(tracedArticle, "/", "%2F"),
		tracedProject, "planted-repo-6b3e"} {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("the export carries %q", bad)
		}
	}
}

// Criterion: with OTEL_* unset there is no traceparent and no export, and the
// same holds when OTEL_SDK_DISABLED=true or OTEL_TRACES_EXPORTER=none
// overrides a configured collector.
func TestCLIWithoutClientTracingSendsNoTraceparentAndExportsNothing(t *testing.T) {
	api := newHeaderAPI(t)
	collector := newOTLPCollector(t, http.StatusOK)
	want := baselineLookup(t, api.srv.URL)
	cases := map[string][]string{
		"OTEL_* unset": nil,
		"sdk disabled": {"OTEL_SDK_DISABLED=true", "OTEL_EXPORTER_OTLP_ENDPOINT=" + collector.srv.URL, "OTEL_TRACES_EXPORTER=otlp"},
		"traces none":  {"OTEL_TRACES_EXPORTER=none", "OTEL_EXPORTER_OTLP_ENDPOINT=" + collector.srv.URL, "OTEL_LOGS_EXPORTER=otlp", "OTEL_METRICS_EXPORTER=otlp"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			before := len(api.seen())
			out, stderr, _, err := runClientBinary(t, env, lookupArgs(api.srv.URL)...)
			if err != nil || out != want {
				t.Fatalf("lookup: err %v, stdout differs from the run without telemetry:\n%s\nstderr:\n%s", err, out, stderr)
			}
			assertNoTraceparent(t, api.seen()[before:])
		})
	}
	if hits, _ := collector.snapshot(); len(hits) != 0 {
		t.Errorf("the collector got %v with client tracing off", hits)
	}
}

// Criterion: configured, every API request carries traceparent and the
// collector gets exactly one export, of traces only, as goldie.mode=cli.
func TestCLIWithClientTracingSendsTraceparentAndExportsOnce(t *testing.T) {
	api := newHeaderAPI(t)
	want := baselineLookup(t, api.srv.URL)
	collector := newOTLPCollector(t, http.StatusOK)
	before := len(api.seen())
	out, stderr, _, err := runClientBinary(t, []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + collector.srv.URL,
		"OTEL_LOGS_EXPORTER=otlp", "OTEL_METRICS_EXPORTER=otlp",
	}, lookupArgs(api.srv.URL)...)
	if err != nil || out != want {
		t.Fatalf("lookup: err %v, stdout differs from the run without telemetry:\n%s\nstderr:\n%s", err, out, stderr)
	}
	ids := traceIDs(t, api.seen()[before:])
	hits, _ := collector.snapshot()
	if hits["/v1/traces"] != 1 {
		t.Errorf("the collector got %d trace exports, want exactly 1 (hits: %v)", hits["/v1/traces"], hits)
	}
	assertClientExport(t, collector, "cli", ids)
}

// Criterion: an unreachable collector (refusing, or accepting and never
// answering) does not hold the command past the bounded flush, and the
// command's output and exit status are what they are without telemetry.
func TestCLIUnreachableCollectorDoesNotDelayTheCommand(t *testing.T) {
	api := newHeaderAPI(t)
	want := baselineLookup(t, api.srv.URL)
	for name, endpoint := range map[string]string{
		"refused": closedServerAddr(),
		"hanging": hangingCollector(t),
	} {
		t.Run(name, func(t *testing.T) {
			out, stderr, took, err := runClientBinary(t, []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + endpoint}, lookupArgs(api.srv.URL)...)
			if err != nil {
				t.Fatalf("lookup failed because of the collector: %v\nstderr:\n%s", err, stderr)
			}
			if took > clientExitBound {
				t.Errorf("lookup took %v with an unreachable collector, want under %v (bounded flush)", took, clientExitBound)
			}
			if out != want {
				t.Errorf("stdout differs from the run without telemetry:\n%s", out)
			}
			if strings.Contains(stderr, strings.TrimPrefix(endpoint, "http://")) {
				t.Errorf("stderr names the collector address (PC-05):\n%s", stderr)
			}
		})
	}
}

// Criterion: SDK notices go to stderr, never stdout. A collector that refuses
// the export makes the SDK report; the CLI's stdout is unchanged.
func TestCLISDKNoticeGoesToStderrNotStdout(t *testing.T) {
	api := newHeaderAPI(t)
	want := baselineLookup(t, api.srv.URL)
	collector := newOTLPCollector(t, http.StatusInternalServerError)
	out, stderr, took, err := runClientBinary(t, []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + collector.srv.URL}, lookupArgs(api.srv.URL)...)
	if err != nil {
		t.Fatalf("lookup failed because of the collector: %v\nstderr:\n%s", err, stderr)
	}
	if took > clientExitBound {
		t.Errorf("lookup took %v, want under %v", took, clientExitBound)
	}
	if out != want {
		t.Errorf("stdout differs from the run without telemetry:\n%s", out)
	}
	if hits, _ := collector.snapshot(); hits["/v1/traces"] == 0 {
		t.Fatalf("the collector got no export, so the SDK had nothing to report (hits: %v)", hits)
	}
	if !strings.Contains(stderr, sdkNotice) {
		t.Errorf("stderr lacks the SDK notice %q:\n%s", sdkNotice, stderr)
	}
	if strings.Contains(stderr, strings.TrimPrefix(collector.srv.URL, "http://")) {
		t.Errorf("stderr names the collector address (PC-05):\n%s", stderr)
	}
}

// mcpRun is one `goldie mcp --transport stdio` session: everything it wrote
// to stdout, line by line, its stderr, and how long it took to exit once its
// stdin was closed.
type mcpRun struct {
	stdout     []string
	stderr     string
	exitsAfter time.Duration
	waitErr    error
}

// runMCPSession initializes an MCP session over stdio, calls get_article
// once, closes stdin and waits for the process to exit.
func runMCPSession(t *testing.T, env []string, api string) mcpRun {
	t.Helper()
	cmd, stderr := mcpCommand(t, env, "--addr", api, "--token", cliToken)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	var mu sync.Mutex
	var lines []string
	responses := make(chan string, 16)
	eof := make(chan struct{})
	go func() {
		defer close(eof)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for sc.Scan() {
			mu.Lock()
			lines = append(lines, sc.Text())
			mu.Unlock()
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(sc.Bytes(), &msg) == nil && msg.Method == "" && len(msg.ID) > 0 {
				select {
				case responses <- string(msg.ID):
				default:
				}
			}
		}
	}()
	send := func(s string) {
		if _, err := io.WriteString(stdin, s+"\n"); err != nil {
			t.Fatalf("write to goldie mcp: %v\nstderr:\n%s", err, stderr)
		}
	}
	await := func(id string) {
		timeout := time.After(20 * time.Second)
		for {
			select {
			case got := <-responses:
				if got == id {
					return
				}
			case <-timeout:
				t.Fatalf("no response %s from goldie mcp\nstderr:\n%s", id, stderr)
			}
		}
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
		`"capabilities":{},"clientInfo":{"name":"test","version":"v0"}}}`)
	await("1")
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_article","arguments":{"id":"` + tracedArticle + `"}}}`)
	await("2")

	closed := time.Now()
	_ = stdin.Close()
	exited := make(chan error, 1)
	go func() { <-eof; exited <- cmd.Wait() }()
	var run mcpRun
	select {
	case run.waitErr = <-exited:
		run.exitsAfter = time.Since(closed)
	case <-time.After(30 * time.Second):
		t.Fatalf("goldie mcp did not exit after stdin closed\nstderr:\n%s", stderr)
	}
	mu.Lock()
	run.stdout = append([]string(nil), lines...)
	mu.Unlock()
	run.stderr = stderr.String()
	return run
}

// assertStdoutIsProtocolOnly fails the test unless every stdout line is a
// JSON-RPC 2.0 message.
func assertStdoutIsProtocolOnly(t *testing.T, lines []string) {
	t.Helper()
	for i, line := range lines {
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg["jsonrpc"] != "2.0" {
			t.Errorf("stdout line %d is not a JSON-RPC message: %q", i, line)
		}
		if strings.Contains(line, "OpenTelemetry") {
			t.Errorf("stdout line %d carries an SDK notice: %q", i, line)
		}
	}
}

// Criterion: goldie mcp over stdio traces its API calls as goldie.mode=mcp:
// the request carries traceparent and the collector gets traces only.
// Without OTEL_* it sends no traceparent.
func TestMCPStdioClientTracingSendsTraceparentAndExportsAsMCP(t *testing.T) {
	api := newHeaderAPI(t)

	run := runMCPSession(t, nil, api.srv.URL)
	assertStdoutIsProtocolOnly(t, run.stdout)
	assertNoTraceparent(t, api.seen())

	collector := newOTLPCollector(t, http.StatusOK)
	before := len(api.seen())
	run = runMCPSession(t, []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + collector.srv.URL,
		"OTEL_LOGS_EXPORTER=otlp", "OTEL_METRICS_EXPORTER=otlp",
	}, api.srv.URL)
	if run.waitErr != nil {
		t.Errorf("goldie mcp exited with %v\nstderr:\n%s", run.waitErr, run.stderr)
	}
	assertStdoutIsProtocolOnly(t, run.stdout)
	ids := traceIDs(t, api.seen()[before:])
	assertClientExport(t, collector, "mcp", ids)
}

// Criterion: SDK notices go to stderr, never stdout, which carries only MCP
// messages; a failing or unreachable collector does not hold the process
// past the bounded flush once its stdin closes.
func TestMCPStdioSDKNoticesNeverReachStdout(t *testing.T) {
	api := newHeaderAPI(t)
	failing := newOTLPCollector(t, http.StatusInternalServerError)
	for name, c := range map[string]struct {
		endpoint   string
		wantNotice bool
	}{
		"collector refuses the export": {failing.srv.URL, true},
		"collector never answers":      {hangingCollector(t), false},
		"collector unreachable":        {closedServerAddr(), false},
	} {
		t.Run(name, func(t *testing.T) {
			run := runMCPSession(t, []string{"OTEL_EXPORTER_OTLP_ENDPOINT=" + c.endpoint}, api.srv.URL)
			if run.waitErr != nil {
				t.Errorf("goldie mcp exited with %v\nstderr:\n%s", run.waitErr, run.stderr)
			}
			assertStdoutIsProtocolOnly(t, run.stdout)
			if run.exitsAfter > clientExitBound {
				t.Errorf("goldie mcp took %v to exit after stdin closed, want under %v (bounded flush)", run.exitsAfter, clientExitBound)
			}
			if c.wantNotice && !strings.Contains(run.stderr, sdkNotice) {
				t.Errorf("stderr lacks the SDK notice %q:\n%s", sdkNotice, run.stderr)
			}
			if strings.Contains(run.stderr, strings.TrimPrefix(c.endpoint, "http://")) {
				t.Errorf("stderr names the collector address (PC-05):\n%s", run.stderr)
			}
		})
	}
	if hits, _ := failing.snapshot(); hits["/v1/traces"] == 0 {
		t.Errorf("the failing collector got no export, so the SDK had nothing to report (hits: %v)", hits)
	}
}
