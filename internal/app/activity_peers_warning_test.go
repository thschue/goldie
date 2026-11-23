package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// Peers without a pod name are logged once per finder (one per process), as
// a count only: no address, no PTR name, no configured name (#398).
func TestActivityPeerFinderWarnsOnceWithTheCountOnly(t *testing.T) {
	r := &fakeResolver{
		hosts: map[string][]string{headless: {"10.9.0.5", "10.9.0.6", "10.9.0.7"}},
		ptrs: map[string][]string{
			"10.9.0.5": {"goldie-0.goldie-headless.goldie.svc.cluster.local."},
			"10.9.0.6": {"10-9-0-6.goldie-headless.goldie.svc.cluster.local."},
			// 10.9.0.7 has no PTR record.
		},
	}
	var buf bytes.Buffer
	f := &activityPeerFinder{resolver: r, logger: slog.New(slog.NewJSONHandler(&buf, nil)), name: headless, port: "8080"}
	for range 3 {
		peers, err := f.find(t.Context())
		if err != nil || len(peers) != 1 || peers[0].Name != "goldie-0" {
			t.Fatalf("find = %+v, %v; want goldie-0 only", peers, err)
		}
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("%d log lines, want one warning per finder:\n%s", len(lines), buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["level"] != "WARN" || rec["skipped"] != float64(2) {
		t.Errorf("record %v, want a warning with skipped 2", rec)
	}
	for k := range rec {
		if k != "time" && k != "level" && k != "msg" && k != "skipped" {
			t.Errorf("the warning carries %q; it carries only the count", k)
		}
	}
	for _, leak := range []string{"10.9.0", "10-9-0", "goldie-headless", "goldie-0", "8080"} {
		if strings.Contains(lines[0], leak) {
			t.Errorf("the warning contains %q: %s", leak, lines[0])
		}
	}
	assertPeerNameIsNoAddress(t, rec["msg"].(string))
}
