package app

import (
	"slices"
	"testing"
)

// A dual-stack pod has an IPv4 and an IPv6 address with the same PTR name: it
// is one peer, at the first address DNS gave, and neither address counts as
// skipped. Two peers of one name would be one replica merged twice (#398).
func TestLookupActivityPeersKeepsOnePeerPerName(t *testing.T) {
	r := &fakeResolver{
		hosts: map[string][]string{headless: {"10.0.0.5", "fd00::5", "10.0.0.6", "10.0.0.5"}},
		ptrs: map[string][]string{
			"10.0.0.5": {"goldie-0.goldie-headless.goldie.svc.cluster.local."},
			"fd00::5":  {"goldie-0.goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.6": {"goldie-1.goldie-headless.goldie.svc.cluster.local."},
		},
	}
	peers, skipped, err := lookupActivityPeers(t.Context(), r, headless, "8080")
	if err != nil {
		t.Fatalf("lookupActivityPeers: %v", err)
	}
	want := []activityPeer{{Name: "goldie-0", URL: "http://10.0.0.5:8080"}, {Name: "goldie-1", URL: "http://10.0.0.6:8080"}}
	if !slices.Equal(peers, want) || skipped != 0 {
		t.Errorf("peers %+v skipped %d, want %+v and 0", peers, skipped, want)
	}
	if len(r.ptrAsked) != 3 {
		t.Errorf("PTR queries %q, want one per distinct address", r.ptrAsked)
	}
}
