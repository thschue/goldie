package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance test for #243, round 1 (ADR 0009): the activity stream's
// payloads are spec components, so the server and the spec cannot drift.
// Every data line an SSE client reads validates against the component for its
// event type: a plain event against components/schemas/ActivityEvent, an
// `event: dropped` notice against components/schemas/ActivityDropped, and
// the `event: live` marker against components/schemas/ActivityLive (#245,
// which also adds replica and late to every event). Both
// schemas close their objects (additionalProperties: false) and require every
// field the wire carries, so an undeclared field or a missing one fails. The
// field names are the wire format as it is.
//
// Responses are validated the way contracttest.Check validates them: the
// spec (contract.SpecJSON) as one jsonschema resource, compiled at a JSON
// pointer. contracttest exports no validator for a component, so the test
// compiles the component itself.

// activityEventFields and activityDroppedFields are the wire format.
var (
	activityEventFields   = []string{"client_version", "id", "kind", "late", "miss", "project", "query", "replica", "time", "token", "trace_id"}
	activityDroppedFields = []string{"dropped"}
)

// componentSchema compiles components/schemas/<name> of the spec and checks
// it is a closed object that declares and requires exactly fields.
func componentSchema(t *testing.T, name string, fields []string) *jsonschema.Schema {
	t.Helper()
	raw, err := contract.SpecJSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Type                 any            `json:"type"`
				Properties           map[string]any `json:"properties"`
				Required             []string       `json:"required"`
				AdditionalProperties *bool          `json:"additionalProperties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s, ok := doc.Components.Schemas[name]
	if !ok {
		t.Fatalf("the spec has no components/schemas/%s: the activity payload is not in the contract (ADR 0009)", name)
	}
	if s.Type != "object" {
		t.Errorf("%s: type %v, want object", name, s.Type)
	}
	if s.AdditionalProperties == nil || *s.AdditionalProperties {
		t.Errorf("%s: additionalProperties is not false, so a field the spec does not declare would pass", name)
	}
	declared := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		declared = append(declared, k)
	}
	sort.Strings(declared)
	if !slices.Equal(declared, fields) {
		t.Errorf("%s declares %v, want the wire fields %v", name, declared, fields)
	}
	required := slices.Clone(s.Required)
	sort.Strings(required)
	if !slices.Equal(required, fields) {
		t.Errorf("%s requires %v, want every wire field %v: the server always sends them", name, required, fields)
	}

	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("spec.json", inst); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("spec.json#/components/schemas/" + name)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return sch
}

// validateStream validates every dispatched event's data against the
// component for its type and returns the decoded data, in order.
func validateStream(t *testing.T, events []sseEvent) []map[string]any {
	t.Helper()
	schemas := map[string]*jsonschema.Schema{
		"message": componentSchema(t, "ActivityEvent", activityEventFields),
		"dropped": componentSchema(t, "ActivityDropped", activityDroppedFields),
		"live":    componentSchema(t, "ActivityLive", nil), // #245: the end of the history
	}
	out := make([]map[string]any, len(events))
	for i, e := range events {
		out[i] = validateEvent(t, i, e, schemas)
	}
	return out
}

// validateEvent validates one event's data and decodes it; nil when it fails.
func validateEvent(t *testing.T, i int, e sseEvent, schemas map[string]*jsonschema.Schema) map[string]any {
	t.Helper()
	sch, ok := schemas[e.Type]
	if !ok {
		t.Errorf("event %d has type %q, which the spec does not describe", i, e.Type)
		return nil
	}
	if e.DataLines != 1 {
		t.Errorf("event %d: data spans %d lines, want one line of JSON: %q", i, e.DataLines, e.Data)
		return nil
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(e.Data))
	if err != nil {
		t.Errorf("event %d: data is not JSON: %v: %q", i, err, e.Data)
		return nil
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("event %d (%s): data does not conform to the spec: %v\n%s", i, e.Type, err, e.Data)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(e.Data), &m); err != nil {
		t.Errorf("event %d: %v", i, err)
		return nil
	}
	return m
}

// Every field populated: an operator's search through the traced handler,
// with a client version and a Project in another case than registered, that
// misses. The stream carries it, and the history's lookup, as ActivityEvents.
func TestActivityEventsConformToTheActivityEventSchema(t *testing.T) {
	col := newOTLPCollector(t)
	env := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": col.srv.URL}
	tel, err := platform.SetupTelemetry(context.Background(), func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatalf("SetupTelemetry: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = tel.Shutdown(ctx)
	})
	logger := tel.WrapLogger(platform.NewLogger(&lockedBuffer{}, slog.LevelDebug, "server"), "server")
	pub, err := PublicHandler(platform.Config{ContentDir: spellingContent, APITokens: namedTokenLines(), RateLimit: roomyRateLimit}, logger)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(tel.WrapHandler(pub))
	t.Cleanup(srv.Close)

	for _, path := range []string{
		"/api/v1/articles/kubernetes?project=github.com/acme/shop",
		"/api/v1/search?q=cobol&project=github.com/ACME/shop",
	} {
		if r, b := do(t, http.MethodGet, srv.URL+path, asClient(operatorToken, "goldie/v1.5.0")); r.StatusCode != http.StatusOK {
			t.Fatalf("fixture: %s: status %d: %s", path, r.StatusCode, b)
		}
	}
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	events := []sseEvent{nextEvent(t, s, 5*time.Second), nextEvent(t, s, 5*time.Second)}
	got := validateStream(t, events)
	if len(got) != 2 {
		t.Fatalf("decoded %d events, want 2", len(got))
	}
	e := got[1]
	for field, want := range map[string]any{
		"kind": "search", "query": "cobol", "project": registeredSpelling, "token": "ci-operator",
		"client_version": "1.5.0", "miss": true, "id": float64(2),
	} {
		if e[field] != want {
			t.Errorf("event %s = %#v, want %#v", field, e[field], want)
		}
	}
	if id, _ := e["trace_id"].(string); !traceIDPattern.MatchString(id) {
		t.Errorf("event trace_id %#v, want the request's 32-hex trace id", e["trace_id"])
	}
	if ts, _ := e["time"].(string); ts == "" {
		t.Errorf("event time %#v, want an RFC 3339 time", e["time"])
	} else if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("event time %q is not RFC 3339: %v", ts, err)
	}
	if got[0]["kind"] != "lookup" || got[0]["miss"] != false {
		t.Errorf("history event %v, want the lookup that hit", got[0])
	}
}

// A dropped notice conforms to ActivityDropped, and the events around it to
// ActivityEvent.
func TestActivityDroppedNoticeConformsToTheActivityDroppedSchema(t *testing.T) {
	pub := streamHandler(t, platform.Config{}, api.DefaultActivityKeepalive)
	if rec := serveAs(t, pub, searchPath("before"), func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: status %d", rec.Code)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := newStallWriter(ctx)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, activityPath, nil)
	bearer(operatorToken)(req)
	served := make(chan struct{})
	go func() {
		defer close(served)
		pub.ServeHTTP(w, req)
	}()
	select {
	case <-w.wrote:
	case <-served:
		t.Fatalf("the stream ended before writing: status %d: %s", w.code, w.bytes())
	case <-time.After(5 * time.Second):
		t.Fatal("the stream wrote nothing within 5s")
	}
	for range api.ActivitySubscriberBuffer + 20 {
		if rec := serveAs(t, pub, searchPath("while stalled"), func(*http.Request) {}); rec.Code != http.StatusOK {
			t.Fatalf("fixture: status %d", rec.Code)
		}
	}
	close(w.gate)
	var events []sseEvent
	deadline := time.Now().Add(5 * time.Second)
	for {
		if rec := serveAs(t, pub, searchPath("after"), func(*http.Request) {}); rec.Code != http.StatusOK {
			t.Fatalf("fixture: status %d", rec.Code)
		}
		time.Sleep(20 * time.Millisecond)
		events, _ = parseSSE(w.bytes())
		if slices.ContainsFunc(events, func(e sseEvent) bool { return strings.Contains(e.Data, `"after"`) }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the subscriber never caught up; it got:\n%s", w.bytes())
		}
	}
	cancel()
	<-served

	if !slices.ContainsFunc(events, func(e sseEvent) bool { return e.Type == "dropped" }) {
		t.Fatalf("no dropped notice; got:\n%s", w.bytes())
	}
	got := validateStream(t, events)
	for i, e := range events {
		if e.Type != "dropped" {
			continue
		}
		if n, _ := got[i]["dropped"].(float64); n < 1 {
			t.Errorf("dropped notice %v, want the number lost, at least 1", got[i])
		}
	}
}
