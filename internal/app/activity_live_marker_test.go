package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #245, slice 1 (PR #378, architect's answers): the
// local activity stream marks the end of its retained history and names its
// replica on every event. The federated parts (#398, #399) were recorded at
// 71a71109fcf7759ac9f547d0bc0a4618fee99647. The contract these tests fix,
// beyond the spec:
//
//	// replicaNameFromHostname is a replica's own name: the hostname's first
//	// label, lower-cased, or "goldie" when that is not a DNS label (1 to 63
//	// of [a-z0-9-]).
//	func replicaNameFromHostname(hostname string) string
//
//	// activityReplicaName is this replica's own name, the replica of every
//	// event it records: the public handler passes it to api.NewActivity
//	// instead of "". Read when the public handler is built, like
//	// the keepalive; it defaults to replicaNameFromHostname(os.Hostname()).
//	var activityReplicaName func() string
//
// The wire format, beyond #243's:
//   - every event's JSON carries "replica", the name of the replica that
//     recorded it, and "late", always false on the local stream (only a merger
//     sets it); both are always sent;
//   - right after the retained history, even an empty one, the stream sends
//     one SSE event of type live whose data is {} (the ActivityLive schema).

// namedStreamHandler builds the public handler with activityReplicaName set
// to name, restoring it afterwards.
func namedStreamHandler(t *testing.T, name string, keepalive time.Duration) *Public {
	t.Helper()
	old := activityReplicaName
	t.Cleanup(func() { activityReplicaName = old })
	activityReplicaName = func() string { return name }
	return streamHandler(t, platform.Config{}, keepalive)
}

// nextOfAny returns the next dispatched event of any type, the live marker
// included, skipping comments.
func nextOfAny(t *testing.T, s *sseLive, within time.Duration) sseEvent {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case it, ok := <-s.items:
			if !ok {
				t.Fatalf("the stream ended while an event was expected; so far:\n%s", s.bytes())
			}
			if !it.comment {
				return it.event
			}
		case <-deadline:
			t.Fatalf("no event within %v; so far:\n%s", within, s.bytes())
		}
	}
}

// isLiveMarker reports whether e is the live marker.
func isLiveMarker(e sseEvent) bool {
	return e.Type == "live" && e.Data == "{}" && e.DataLines == 1
}

// The live marker: the local stream sends `event: live` with data {}
// straight after its retained history, even an empty one, and only once.
func TestTheLocalStreamMarksTheEndOfItsHistory(t *testing.T) {
	pub := namedStreamHandler(t, "goldie-a", 50*time.Millisecond)
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(t.Context())
	resp, s, body := openStream(t, ctx, http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("with no history: status %d: %s", resp.StatusCode, body)
	}
	if first := nextOfAny(t, s, 5*time.Second); !isLiveMarker(first) {
		t.Errorf("with no history: the first event is %+v, want the live marker (event: live, data: {})", first)
	}
	cancel()
	waitReadEnds(t, s)
	waitHubEmpty(t, pub.Activity())

	for _, q := range []string{"hist-1", "hist-2"} {
		if rec := serveAs(t, pub, searchPath(q), func(*http.Request) {}); rec.Code != http.StatusOK {
			t.Fatalf("fixture: search %q: status %d", q, rec.Code)
		}
	}
	resp, s, body = openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var seen []sseEvent
	for len(seen) < 3 {
		seen = append(seen, nextOfAny(t, s, 5*time.Second))
	}
	if !isLiveMarker(seen[2]) || seen[0].Type != "message" || seen[1].Type != "message" {
		t.Fatalf("the stream opens with %+v, want the two history events, then the live marker", seen)
	}
	if rec := serveAs(t, pub, searchPath("after-marker"), func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: status %d", rec.Code)
	}
	if e := decodeEvent(t, nextOfAny(t, s, 5*time.Second)); e.Query != "after-marker" {
		t.Errorf("after the marker: query %q, want after-marker", e.Query)
	}
	time.Sleep(300 * time.Millisecond) // keepalives flow; no second marker may come
	events, _ := parseSSE(s.bytes())
	if n := len(slices.DeleteFunc(slices.Clone(events), func(e sseEvent) bool { return e.Type != "live" })); n != 1 {
		t.Errorf("%d live markers, want exactly one:\n%s", n, s.bytes())
	}
}

// Every event on the local stream carries replica, this replica's name, and
// late, sent and false; there is no field beyond #243's but these two; ids
// are consecutive from 1; and the one non-event is the live marker after the
// history.
func TestTheLocalStreamCarriesReplicaAndLateOnEveryEvent(t *testing.T) {
	pub := namedStreamHandler(t, "goldie-a", api.DefaultActivityKeepalive)
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	if rec := serveAs(t, pub, searchPath("a-1"), func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: status %d", rec.Code)
	}
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	seen := []sseEvent{nextOfAny(t, s, 5*time.Second), nextOfAny(t, s, 5*time.Second)}
	if rec := serveAs(t, pub, "/api/v1/articles/agent-loops", func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: status %d", rec.Code)
	}
	seen = append(seen, nextOfAny(t, s, 5*time.Second))
	if !isLiveMarker(seen[1]) {
		t.Fatalf("the local stream: %+v, want a-1, the live marker, then the lookup", seen)
	}
	known := map[string]bool{"id": true, "time": true, "kind": true, "query": true, "project": true, "token": true,
		"client_version": true, "trace_id": true, "miss": true, "replica": true, "late": true, "via": true}
	known["served"], known["latency_us"] = true, true // #806: optional fields beyond the original set
	for i, e := range []sseEvent{seen[0], seen[2]} {
		if e.Type != "message" || e.DataLines != 1 {
			t.Errorf("event %d is %+v, want one line of JSON without a type", i, e)
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(e.Data), &m); err != nil {
			t.Fatalf("event %d is not JSON: %q", i, e.Data)
		}
		for k := range m {
			if !known[k] {
				t.Errorf("the local stream's event carries the field %q beyond replica and late: %s", k, e.Data)
			}
		}
		if id, _ := m["id"].(float64); id != float64(i+1) {
			t.Errorf("event %d has id %v, want %d", i, m["id"], i+1)
		}
		if late, ok := m["late"].(bool); !ok || late {
			t.Errorf("event %d has late %v, want it sent and false: %s", i, m["late"], e.Data)
		}
		if m["replica"] != "goldie-a" {
			t.Errorf("event %d has replica %v, want this replica's name goldie-a: %s", i, m["replica"], e.Data)
		}
	}
}

// A replica's own name: the hostname's first label, lower-cased, or goldie
// when that is not a DNS label. It never carries a domain, and the default of
// activityReplicaName is it over os.Hostname().
func TestReplicaNameIsTheHostnamesFirstLabel(t *testing.T) {
	for host, want := range map[string]string{
		"goldie-0":                     "goldie-0",
		"Goldie-1":                     "goldie-1",
		"goldie-2.goldie-headless.svc": "goldie-2",
		"web-7d9f8c-x2x4z":             "web-7d9f8c-x2x4z",
		"":                             "goldie",
		"goldie_3":                     "goldie",
		".goldie":                      "goldie",
		strings.Repeat("a", 64):        "goldie",
		"g\u00f6ldie":                  "goldie",
		"goldie\u200b":                 "goldie",
	} {
		if got := replicaNameFromHostname(host); got != want {
			t.Errorf("replicaNameFromHostname(%q) = %q, want %q", host, got, want)
		}
	}
	h, _ := os.Hostname()
	if got, want := activityReplicaName(), replicaNameFromHostname(h); got != want {
		t.Errorf("activityReplicaName() = %q, want %q from os.Hostname()", got, want)
	}
}

// Spec first: ActivityEvent declares replica (a string) and late (a boolean),
// both required in the closed schema; ActivityLive is a closed schema listed
// by $ref in the stream's contentSchema with the other payloads; the
// operation's description explains the live marker; and replica's examples
// name no address.
func TestServedSpecDeclaresTheLiveMarkerAndReplicaAndLate(t *testing.T) {
	doc := specDoc(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	event, _ := schemas["ActivityEvent"].(map[string]any)
	props, _ := event["properties"].(map[string]any)
	for field, typ := range map[string]string{"replica": "string", "late": "boolean"} {
		p := contracttest.ResolveRef(doc, props[field])
		if p == nil || p["type"] != typ {
			t.Errorf("ActivityEvent.%s is %v, want a %s", field, p, typ)
		}
	}
	componentSchema(t, "ActivityEvent", activityEventFields) // closed, every field required
	for _, f := range []string{"replica", "late"} {
		if !slices.Contains(activityEventFields, f) {
			t.Errorf("the wire fields %v do not include %s", activityEventFields, f)
		}
	}
	componentSchema(t, "ActivityLive", nil)

	replica := contracttest.ResolveRef(doc, props["replica"])
	examples, _ := replica["examples"].([]any)
	if ex, ok := replica["example"]; ok {
		examples = append(examples, ex)
	}
	if len(examples) == 0 {
		t.Error("ActivityEvent.replica has no example")
	}
	for _, ex := range examples {
		s, _ := ex.(string)
		if host, _, err := net.SplitHostPort(s); err == nil || net.ParseIP(s) != nil || net.ParseIP(host) != nil {
			t.Errorf("ActivityEvent.replica example %q is an address", s)
		}
		for kind, re := range leaks {
			if m := re.FindString(s); m != "" {
				t.Errorf("ActivityEvent.replica example %q contains a %s (%q)", s, kind, m)
			}
		}
	}

	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[activityPath].(map[string]any)
	op, _ := item["get"].(map[string]any)
	responses, _ := op["responses"].(map[string]any)
	content, _ := contracttest.ResolveRef(doc, responses["200"])["content"].(map[string]any)
	stream, _ := content["text/event-stream"].(map[string]any)
	streamSchema, _ := stream["schema"].(map[string]any)
	cs, _ := streamSchema["contentSchema"].(map[string]any)
	oneOf, _ := cs["oneOf"].([]any)
	refs := map[string]bool{}
	for _, s := range oneOf {
		if m, _ := s.(map[string]any); m != nil {
			ref, _ := m["$ref"].(string)
			refs[ref] = true
		}
	}
	for _, name := range []string{"ActivityEvent", "ActivityDropped", "ActivityLive"} {
		if !refs["#/components/schemas/"+name] {
			t.Errorf("the stream's contentSchema oneOf %v does not list %s", oneOf, name)
		}
	}
	desc, _ := op["description"].(string)
	if !strings.Contains(desc, "`live`") {
		t.Errorf("GET %s's description does not explain the `live` marker", activityPath)
	}
}
