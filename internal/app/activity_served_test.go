package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #806: an ActivityEvent carries the Reference of the
// Item served (`served`) and the handler time in microseconds (`latency_us`),
// on the stream, through federation, and bounded when a peer sends them. The
// seam is the HTTP contract: the activity stream the public handler serves.

// servedEvents serves each path as the operator, then reads that many events
// from the activity stream's history and returns their decoded data.
func servedEvents(t *testing.T, contentDir string, paths []string) []map[string]any {
	t.Helper()
	pub := activityHandler(t, platform.Config{ContentDir: contentDir})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	for _, p := range paths {
		do(t, http.MethodGet, srv.URL+p, asClient(operatorToken, "goldie/v1.5.0"))
	}
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	out := make([]map[string]any, 0, len(paths))
	for range paths {
		e := nextEvent(t, s, 5*time.Second)
		var m map[string]any
		if err := json.Unmarshal([]byte(e.Data), &m); err != nil {
			t.Fatalf("event is not JSON: %q", e.Data)
		}
		out = append(out, m)
	}
	return out
}

// topResult is the Reference of the first result of a search, read from the
// search's own answer.
func topResult(t *testing.T, h http.Handler, q string) string {
	t.Helper()
	rec := serveAs(t, h, searchPath(q), func(*http.Request) {})
	var res struct {
		Results []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || len(res.Results) == 0 {
		t.Fatalf("fixture: search %q: status %d, no results: %s", q, rec.Code, rec.Body.String())
	}
	return res.Results[0].Kind + ":" + res.Results[0].ID
}

// Criterion 1: served is the Reference of the top result of a search and of
// the Item of a lookup or get.
func TestActivityEventServedIsTheReferenceOfTheItemServed(t *testing.T) {
	probe := activityHandler(t, platform.Config{ContentDir: skillsAgentsContent})
	wantSearch := topResult(t, probe, "deploy")
	events := servedEvents(t, skillsAgentsContent, []string{
		searchPath("deploy"),
		"/api/v1/articles/intro",
		"/api/v1/skills/deploy",
		"/api/v1/agents/reviewer",
	})
	for i, want := range []string{wantSearch, "article:intro", "skill:deploy", "agent:reviewer"} {
		if got := events[i]["served"]; got != want {
			t.Errorf("event %d (%v %v): served %#v, want %q", i, events[i]["kind"], events[i]["query"], got, want)
		}
	}
}

// Criterion 1: served is absent on a miss.
func TestActivityEventServedIsAbsentOnAMiss(t *testing.T) {
	events := servedEvents(t, skillsAgentsContent, []string{
		searchPath("zzqqxxnothing"),
		"/api/v1/articles/nope",
		"/api/v1/skills/nope",
		"/api/v1/agents/nope",
	})
	for i, e := range events {
		if e["miss"] != true {
			t.Fatalf("fixture: event %d is not a miss: %v", i, e)
		}
		if v, ok := e["served"]; ok {
			t.Errorf("event %d (%v): a miss carries served %#v, want the field absent", i, e["query"], v)
		}
	}
}

// Criterion 2: latency_us is an integer, never negative, on every recorded
// event, hit or miss.
func TestActivityEventLatencyIsANonNegativeInteger(t *testing.T) {
	events := servedEvents(t, skillsAgentsContent, []string{searchPath("deploy"), "/api/v1/articles/nope"})
	for i, e := range events {
		v, ok := e["latency_us"].(float64)
		if !ok {
			t.Errorf("event %d: latency_us %#v, want a number", i, e["latency_us"])
			continue
		}
		if v < 0 || v != math.Trunc(v) {
			t.Errorf("event %d: latency_us %v, want a non-negative integer", i, v)
		}
	}
}

// Criterion 3: both fields pass through federation unchanged from a
// well-behaved replica.
func TestActivityEventServedAndLatencyPassThroughFederation(t *testing.T) {
	a, b, _ := abc(t)
	want := topResult(t, b.pub, "kubernetes")
	record(t, b, "kubernetes")
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	ev := r.until(t, 10*time.Second, "b's search", func(e sseEvent) bool {
		w, ok := asEvent(e)
		return ok && w.Query == "kubernetes" && w.replica() == "goldie-b"
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &m); err != nil {
		t.Fatal(err)
	}
	if m["served"] != want {
		t.Errorf("federated served %#v, want %q", m["served"], want)
	}
	if v, ok := m["latency_us"].(float64); !ok || v < 0 {
		t.Errorf("federated latency_us %#v, want a non-negative number", m["latency_us"])
	}
}

// Criterion 3, peer input is untrusted: a peer's served is cut to a bounded
// length and a negative latency_us is clamped to zero, and the event still
// shows.
func TestAPeersServedIsBoundedAndItsNegativeLatencyIsClamped(t *testing.T) {
	a, _, _ := abc(t)
	long := strings.Repeat("x", 5000)
	peer := hostilePeer(t, func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		_, _ = fmt.Fprint(w, liveMarker)
		_ = rc.Flush()
		_, _ = fmt.Fprintf(w, `data: {"id":1,"time":"2026-01-01T00:00:00Z","kind":"search","query":"hostile","project":"","token":"t","client_version":"","trace_id":"","miss":false,"replica":"goldie-bad","late":false,"served":"%s","latency_us":-5}`+"\n\n", long)
		_, _ = fmt.Fprint(w, `data: {"id":2,"time":"2026-01-01T00:00:01Z","kind":"search","query":"hostile-min","project":"","token":"t","client_version":"","trace_id":"","miss":false,"replica":"goldie-bad","late":false,"served":"article:ok","latency_us":-9223372036854775808}`+"\n\n")
		_ = rc.Flush()
	})
	useResolver(t, func(_ context.Context, n string) ([]activityPeer, error) {
		if n != peersName {
			return nil, errors.New(resolverFailure)
		}
		return []activityPeer{{Name: "goldie-a", URL: a.srv.URL}, {Name: "goldie-bad", URL: peer.URL}}, nil
	})
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	decode := func(q string) map[string]any {
		ev := r.until(t, 20*time.Second, q, query(q))
		var m map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	long1, min1 := decode("hostile"), decode("hostile-min")
	if s, _ := long1["served"].(string); len(s) == 0 || len(s) > 1024 {
		t.Errorf("a peer's served of 5000 bytes shows as %d bytes, want a non-empty prefix of at most 1024", len(s))
	}
	for q, m := range map[string]map[string]any{"hostile": long1, "hostile-min": min1} {
		if v, present := m["latency_us"]; present && v != float64(0) {
			t.Errorf("%s: a peer's negative latency_us shows as %#v, want 0 or absent", q, v)
		}
	}
}
