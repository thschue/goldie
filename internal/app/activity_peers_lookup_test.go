package app

import (
	"context"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Acceptance tests for #245, the peer names (architect, PR #378). A peer is
// found by an A lookup of GOLDIE_ACTIVITY_PEERS and a PTR lookup per address;
// its name is the first label of the PTR answer, lower-cased, so the name
// clients see is the replica's identity (a StatefulSet pod name), never its
// address and never what the peer says about itself. The contract these
// tests fix:
//
//	// peerResolver is what lookupActivityPeers needs of DNS; *net.Resolver
//	// satisfies it.
//	type peerResolver interface {
//		LookupHost(ctx context.Context, host string) ([]string, error)
//		LookupAddr(ctx context.Context, addr string) ([]string, error)
//	}
//
//	// lookupActivityPeers resolves name to the peers: for each address of
//	// name, the first PTR answer of the form <label>.<name's labels>[.more]
//	// whose label is a DNS label (1 to 63 of [a-z0-9-] after lower-casing)
//	// and not a dashed IP (10-1-2-3, CoreDNS's name for an endpoint without
//	// a hostname) gives activityPeer{Name: label, URL: "http://" +
//	// net.JoinHostPort(addr, port)}. An address without such an answer is
//	// skipped and counted in skipped, never named after its address. err is
//	// the A lookup's failure only.
//	func lookupActivityPeers(ctx context.Context, r peerResolver, name, port string) (peers []activityPeer, skipped int, err error)
//
//	// replicaNameFromHostname is a replica's own name: the hostname's first
//	// label, lower-cased, or "goldie" when that is not a DNS label.
//	// activityReplicaName defaults to it over os.Hostname().
//	func replicaNameFromHostname(hostname string) string
//
// The server logs one warning per process with the count of skipped peers
// and nothing else; review checks that.

var _ peerResolver = (*net.Resolver)(nil)

// fakeResolver answers from maps and counts its calls.
type fakeResolver struct {
	mu    sync.Mutex
	hosts map[string][]string
	ptrs  map[string][]string
	calls int
}

func (f *fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	addrs, ok := f.hosts[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, Server: "10.96.0.10:53", IsNotFound: true}
	}
	return addrs, nil
}

func (f *fakeResolver) LookupAddr(_ context.Context, addr string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names, ok := f.ptrs[addr]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: addr, Server: "10.96.0.10:53", IsNotFound: true}
	}
	return names, nil
}

const headless = "goldie-headless.goldie.svc"

// The PTR rules: a pod name under the configured name is the peer's name,
// lower-cased, with the URL built from the address and this replica's port
// (IPv6 in brackets). A dashed IP, a name under another suffix, the
// configured name itself, an invalid or overlong label, and an address with
// no PTR are skipped and counted.
func TestLookupActivityPeersNamesPeersByTheirPTRLabel(t *testing.T) {
	r := &fakeResolver{
		hosts: map[string][]string{headless: {
			"10.0.0.5", "10.0.0.6", "10.0.0.7", "10.0.0.8", "10.0.0.9", "10.0.0.10",
			"10.0.0.11", "10.0.0.12", "10.0.0.13", "10.0.0.14", "fd00::15",
		}},
		ptrs: map[string][]string{
			"10.0.0.5":  {"goldie-0.goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.6":  {"Goldie-1.goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.7":  {"10-0-0-7.goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.8":  {"goldie-3.other-headless.goldie.svc.cluster.local."},
			"10.0.0.10": {"goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.11": {"goldie_4.goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.12": {strings.Repeat("g", 64) + ".goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.13": {"goldie-6.xgoldie-headless.goldie.svc.cluster.local."},
			"10.0.0.14": {"goldie-7.goldie-headless.goldie.svc"},
			"fd00::15":  {"goldie-8.goldie-headless.goldie.svc.cluster.local."},
			// 10.0.0.9 has no PTR record.
		},
	}
	peers, skipped, err := lookupActivityPeers(t.Context(), r, headless, "8080")
	if err != nil {
		t.Fatalf("lookupActivityPeers: %v", err)
	}
	slices.SortFunc(peers, func(a, b activityPeer) int { return strings.Compare(a.Name, b.Name) })
	want := []activityPeer{
		{Name: "goldie-0", URL: "http://10.0.0.5:8080"},
		{Name: "goldie-1", URL: "http://10.0.0.6:8080"},
		{Name: "goldie-7", URL: "http://10.0.0.14:8080"},
		{Name: "goldie-8", URL: "http://[fd00::15]:8080"},
	}
	if !slices.Equal(peers, want) {
		t.Errorf("peers %+v, want %+v", peers, want)
	}
	if skipped != 7 {
		t.Errorf("skipped %d, want 7: a dashed IP, a wrong suffix, the configured name itself, an invalid label, an overlong label, a suffix that is not a whole label, and no PTR", skipped)
	}
	for _, p := range peers {
		assertNameIsNoAddress(t, p.Name)
	}
}

// A Deployment-only install (every PTR a dashed IP) has no named peers: the
// lookup returns none, no error, and the count; ?replicas=all is then the
// local stream.
func TestLookupActivityPeersOfADeploymentFindsNoPeers(t *testing.T) {
	r := &fakeResolver{
		hosts: map[string][]string{headless: {"10.1.2.3", "10.1.2.4"}},
		ptrs: map[string][]string{
			"10.1.2.3": {"10-1-2-3.goldie-headless.goldie.svc.cluster.local."},
			"10.1.2.4": {"10-1-2-4.goldie-headless.goldie.svc.cluster.local."},
		},
	}
	peers, skipped, err := lookupActivityPeers(t.Context(), r, headless, "8080")
	if err != nil || len(peers) != 0 || skipped != 2 {
		t.Errorf("peers %+v skipped %d err %v; want none, 2 skipped, no error", peers, skipped, err)
	}
}

// A failed A lookup is an error and no peers.
func TestLookupActivityPeersReportsAFailedLookup(t *testing.T) {
	r := &fakeResolver{hosts: map[string][]string{}}
	peers, _, err := lookupActivityPeers(t.Context(), r, headless, "8080")
	if err == nil || len(peers) != 0 {
		t.Errorf("peers %+v err %v; want an error and no peers", peers, err)
	}
}

// A replica's own name: the hostname's first label, lower-cased, or goldie
// when that is not a DNS label. It never carries a domain.
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
