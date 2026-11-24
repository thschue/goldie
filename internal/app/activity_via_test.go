package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/contract/contracttest"
)

// Acceptance tests for #805: activity events carry via, the surface that
// asked (mcp, cli, ui or other; empty when unknown). The seam is the public
// handler's activity stream, local and merged (?replicas=all), read as an SSE
// client reads it. via is an optional string in the spec, never an enum.

// viaOf is the via of an event's data: "" when the field is absent or empty.
func viaOf(t *testing.T, e sseEvent) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(e.Data), &m); err != nil {
		t.Fatalf("event data is not JSON: %v: %q", err, e.Data)
	}
	v, present := m["via"]
	if !present {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("via is %#v, want a string: %s", v, e.Data)
	}
	return s
}

// historyVias reads the retained history of n's local stream and returns the
// via of each event by its query.
func historyVias(t *testing.T, n *replicaNode) map[string]string {
	t.Helper()
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, n.srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	out := map[string]string{}
	for {
		e := nextOfAny(t, s, 5*time.Second)
		if isLiveMarker(e) {
			return out
		}
		out[decodeEvent(t, e).Query] = viaOf(t, e)
	}
}

// Criterion 1: ActivityEvent declares via as a string that is not required
// and has no enum, so a later value breaks no client.
func TestServedSpecDeclaresViaAsAnOptionalString(t *testing.T) {
	doc := specDoc(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	event, _ := schemas["ActivityEvent"].(map[string]any)
	props, _ := event["properties"].(map[string]any)
	via := contracttest.ResolveRef(doc, props["via"])
	if via == nil || via["type"] != "string" {
		t.Fatalf("ActivityEvent.via is %v, want a string", via)
	}
	if _, ok := via["enum"]; ok {
		t.Errorf("ActivityEvent.via has an enum %v: it is a string so a later value breaks no client", via["enum"])
	}
	required, _ := event["required"].([]any)
	for _, r := range required {
		if r == "via" {
			t.Errorf("ActivityEvent requires via: it is optional, empty when unknown")
		}
	}
}

// Criterion 2: a request from the SPA (session cookie) records ui, one from
// the CLI (X-Goldie-Client) records cli, one through /mcp records mcp, and a
// request that is none of them records other.
func TestActivityEventsRecordTheSurfaceThatAsked(t *testing.T) {
	n := startReplicas(t, nodeSpec{name: "goldie-a", noPeers: true})[0]
	ck := cookieOf(t, n, operatorToken)
	for q, mutate := range map[string]func(*http.Request){
		"via-ui":    withCookie(ck),
		"via-cli":   asClient(readerToken, "goldie/v1.5.0"),
		"via-other": bearer(readerToken),
	} {
		if resp, b := do(t, http.MethodGet, n.srv.URL+searchPath(q), mutate); resp.StatusCode != http.StatusOK {
			t.Fatalf("fixture: search %q: status %d: %s", q, resp.StatusCode, b)
		}
	}
	cs := mustConnectHTTP(t, n.srv.URL+mcpEndpoint, withBearer(readerToken, http.DefaultTransport))
	if text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "via-mcp"}); isErr {
		t.Fatalf("fixture: search_knowledge: %s", text)
	}
	got := historyVias(t, n)
	for q, want := range map[string]string{"via-ui": "ui", "via-cli": "cli", "via-mcp": "mcp", "via-other": "other"} {
		if v, ok := got[q]; !ok || v != want {
			t.Errorf("event %q has via %q (recorded: %t), want %q; all: %v", q, v, ok, want, got)
		}
	}
}

// Criterion 3: via passes through the merged stream from the replica that
// recorded it, this one's and a peer's.
func TestMergedStreamCarriesViaOfEveryReplica(t *testing.T) {
	a, b, _ := abc(t)
	for _, n := range []*replicaNode{a, b} {
		if rec := serveAs(t, n.pub, searchPath(n.name+"-via-cli"), asClient(readerToken, "goldie/v1.5.0")); rec.Code != http.StatusOK {
			t.Fatalf("fixture: status %d", rec.Code)
		}
	}
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	for _, q := range []string{"goldie-a-via-cli", "goldie-b-via-cli"} {
		if len(eventsFor(r.seen, q)) == 0 {
			r.until(t, 10*time.Second, q, query(q))
		}
	}
	for _, e := range r.seen {
		if w, ok := asEvent(e); ok && (w.Query == "goldie-a-via-cli" || w.Query == "goldie-b-via-cli") {
			if v := viaOf(t, e); v != "cli" {
				t.Errorf("merged event %q has via %q, want cli", w.Query, v)
			}
		}
	}
}

// Criterion 3: a peer's via outside the documented set, hostile text
// included, becomes other; a documented one passes; a peer without the field
// still renders, with no via.
func TestAPeersViaOutsideTheDocumentedSetBecomesOther(t *testing.T) {
	a, b, c := abc(t)
	sent := []struct{ query, via string }{ // via is JSON; "" sends no via field
		{"peer-mcp", `"mcp"`},
		{"peer-ui", `"ui"`},
		{"peer-hook", `"hook"`},
		{"peer-hostile", "\"<script>alert(1)</script>\\u202e\""},
		{"peer-none", ""},
	}
	peer := hostilePeer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, liveMarker)
		for i, e := range sent {
			field := ""
			if e.via != "" {
				field = `,"via":` + e.via
			}
			_, _ = fmt.Fprintf(w, `data: {"id":%d,"time":%q,"kind":"search","query":%q,"project":"","token":"t","client_version":"","trace_id":"","miss":false,"replica":"goldie-via","late":false%s}`+"\n\n",
				i+1, time.Now().UTC().Format(time.RFC3339Nano), e.query, field)
		}
		_ = http.NewResponseController(w).Flush()
	})
	useResolver(t, func(_ context.Context, name string) ([]activityPeer, error) {
		if name != peersName {
			return nil, errors.New(resolverFailure)
		}
		return []activityPeer{{Name: "goldie-a", URL: a.srv.URL}, {Name: "goldie-b", URL: b.srv.URL},
			{Name: "goldie-via", URL: peer.URL}, {Name: "goldie-c", URL: c.srv.URL}}, nil
	})
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	for _, e := range sent {
		if len(eventsFor(r.seen, e.query)) == 0 {
			r.until(t, 15*time.Second, e.query, query(e.query))
		}
	}
	want := map[string]string{"peer-mcp": "mcp", "peer-ui": "ui", "peer-hook": "other", "peer-hostile": "other", "peer-none": ""}
	for _, e := range r.seen {
		if w, ok := asEvent(e); ok {
			if via, tracked := want[w.Query]; tracked {
				if got := viaOf(t, e); got != via {
					t.Errorf("peer event %q has via %q, want %q: %s", w.Query, got, via, e.Data)
				}
			}
		}
	}
}
