package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance tests for #813: an ActivityEvent gains an optional `nearest` (a
// list of strings): on a search miss, at most 3 keywords near the query. It is
// absent on a hit and when nothing is near. A peer's `nearest` is untrusted
// and bounded. The seam is the HTTP contract: the activity stream the public
// handler serves.

// nearestOf is the `nearest` of an event, and whether the field is present.
func nearestOf(t *testing.T, e map[string]any) ([]string, bool) {
	t.Helper()
	raw, ok := e["nearest"]
	if !ok {
		return nil, false
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("nearest %#v, want a list of strings", raw)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("nearest %#v holds a non-string", raw)
		}
		out = append(out, s)
	}
	return out, true
}

// Criterion 2: a search miss close to a keyword carries it in nearest, at
// most 3.
func TestActivityEventOfASearchMissNamesTheNearestKeywords(t *testing.T) {
	events := servedEvents(t, skillsAgentsContent, []string{searchPath("kubernets")})
	if events[0]["miss"] != true {
		t.Fatalf("fixture: event is not a miss: %v", events[0])
	}
	got, ok := nearestOf(t, events[0])
	if !ok || len(got) == 0 || len(got) > 3 {
		t.Fatalf("nearest %#v (present %v), want 1 to 3 keywords", got, ok)
	}
	if got[0] != "kubernetes" {
		t.Errorf("nearest %q, want kubernetes first", got)
	}
}

// Criterion 2: nearest is absent on a hit, on a miss that is close to
// nothing, and on a lookup miss.
func TestActivityEventNearestIsAbsentWithoutASuggestion(t *testing.T) {
	events := servedEvents(t, skillsAgentsContent, []string{
		searchPath("deploy"),
		searchPath("zzqqxxnothing"),
		"/api/v1/articles/nope",
	})
	for i, e := range events {
		if got, ok := nearestOf(t, e); ok && len(got) != 0 {
			t.Errorf("event %d (%v): nearest %q, want it absent", i, e["query"], got)
		}
	}
}

// Criterion 2: peer input is bounded. A peer's nearest is cut to 3 entries and
// each entry to a bounded length, and the event still shows.
func TestAPeersNearestIsBounded(t *testing.T) {
	a, _, _ := abc(t)
	long := strings.Repeat("x", 5000)
	many := make([]string, 0, 200)
	for i := range 200 {
		many = append(many, fmt.Sprintf("%q", fmt.Sprintf("kw%d", i)))
	}
	peer := hostilePeer(t, func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		_, _ = fmt.Fprint(w, liveMarker)
		_ = rc.Flush()
		_, _ = fmt.Fprintf(w, `data: {"id":1,"time":"2026-01-01T00:00:00Z","kind":"search","query":"hostile-many","project":"","token":"t","client_version":"","trace_id":"","miss":true,"replica":"goldie-bad","late":false,"nearest":[%s]}`+"\n\n", strings.Join(many, ","))
		_, _ = fmt.Fprintf(w, `data: {"id":2,"time":"2026-01-01T00:00:01Z","kind":"search","query":"hostile-long","project":"","token":"t","client_version":"","trace_id":"","miss":true,"replica":"goldie-bad","late":false,"nearest":["%s"]}`+"\n\n", long)
		_ = rc.Flush()
	})
	useResolver(t, func(_ context.Context, n string) ([]activityPeer, error) {
		if n != peersName {
			return nil, errors.New(resolverFailure)
		}
		return []activityPeer{{Name: "goldie-a", URL: a.srv.URL}, {Name: "goldie-bad", URL: peer.URL}}, nil
	})
	r := openFederated(t, t.Context(), a, bearer(operatorToken))
	decode := func(q string) []string {
		ev := r.until(t, 20*time.Second, q, query(q))
		var m map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &m); err != nil {
			t.Fatal(err)
		}
		got, _ := nearestOf(t, m)
		return got
	}
	if got := decode("hostile-many"); len(got) == 0 || len(got) > 3 {
		t.Errorf("a peer's nearest of 200 entries shows as %d, want 1 to 3", len(got))
	}
	long1 := decode("hostile-long")
	if len(long1) == 0 {
		t.Error("a peer's nearest of one 5000-byte entry shows as none, want it kept, cut")
	}
	for _, k := range long1 {
		if len(k) == 0 || len(k) > 1024 {
			t.Errorf("a peer's nearest entry of 5000 bytes shows as %d bytes, want a non-empty prefix of at most 1024", len(k))
		}
	}
}
