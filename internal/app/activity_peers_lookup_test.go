package app

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Acceptance tests for #245, slice 2 (#398): the peer names (architect, PR
// #378). A peer is found by an A lookup of GOLDIE_ACTIVITY_PEERS and a PTR
// lookup per address;
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
//	// whose label is a DNS label as written (1 to 63 ASCII letters, digits
//	// and hyphens, neither first nor last a hyphen), lower-cased,
//	// and not a dashed IP (10-1-2-3, CoreDNS's name for an endpoint without
//	// a hostname) gives activityPeer{Name: label, URL: "http://" +
//	// net.JoinHostPort(addr, port)}. An address without such an answer is
//	// skipped and counted in skipped, never named after its address. err is
//	// the A lookup's failure only.
//	func lookupActivityPeers(ctx context.Context, r peerResolver, name, port string) (peers []activityPeer, skipped int, err error)
//
// replicaNameFromHostname (slice 1, activity_replica.go) owns the DNS-label
// rule; the PTR label check reuses it, so the two cannot diverge. The lookup
// also drops this host's own interface addresses (not counted in skipped),
// takes peer addresses only from the A answers of the configured name (never
// from a PTR answer, never an answer that is not an IP address), gives the
// resolver a context with a deadline even when the caller's has none, and
// caps the peers it resolves. The server logs one warning per process with the
// count of skipped peers and nothing else; that has no seam, so review checks
// it (PR #378).

var _ peerResolver = (*net.Resolver)(nil)

// fakeResolver answers from maps and records what it was asked, and whether
// each call's context had a deadline.
type fakeResolver struct {
	mu         sync.Mutex
	hosts      map[string][]string
	ptrs       map[string][]string
	calls      int
	asked      []string // the names given to LookupHost
	ptrAsked   []string // the addresses given to LookupAddr
	noDeadline int      // calls whose context had no deadline
}

func (f *fakeResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.asked = append(f.asked, host)
	if _, ok := ctx.Deadline(); !ok {
		f.noDeadline++
	}
	addrs, ok := f.hosts[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, Server: "10.96.0.10:53", IsNotFound: true}
	}
	return addrs, nil
}

func (f *fakeResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ptrAsked = append(f.ptrAsked, addr)
	if _, ok := ctx.Deadline(); !ok {
		f.noDeadline++
	}
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
		assertPeerNameIsNoAddress(t, p.Name)
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

// assertPeerNameIsNoAddress fails when a peer name, which clients see, carries
// an address, a port or a hostname (leaks, app_test.go).
func assertPeerNameIsNoAddress(t *testing.T, name string) {
	t.Helper()
	for kind, re := range leaks {
		if m := re.FindString(name); m != "" {
			t.Errorf("the peer name %q contains a %s (%q)", name, kind, m)
		}
	}
}

// One DNS-label rule (architect, PR #378): the PTR label is accepted exactly
// when replicaNameFromHostname accepts it as a hostname, lower-cased the same
// way. Each label below sits where a second copy of the rule tends to differ:
// a leading or trailing hyphen, 63 and 64 bytes, upper case (lowered before
// the check, never rejected for it), and runes outside ASCII, including the
// Kelvin sign U+212A, which strings.ToLower turns into an ASCII k: it is not a
// DNS label as written, so neither rule may accept it.
func TestLookupActivityPeersUsesTheHostnameRuleForThePTRLabel(t *testing.T) {
	for _, c := range []struct {
		label string
		want  string // the peer's name, or "" when the label is refused
	}{
		{"goldie-0", "goldie-0"},
		{"GOLDIE-1", "goldie-1"},
		{"Goldie-2", "goldie-2"},
		{"a", "a"},
		{"7", "7"},
		{"xn--gldie-kva", "xn--gldie-kva"},
		{strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{strings.Repeat("B", 63), strings.Repeat("b", 63)},
		{strings.Repeat("a", 64), ""},
		{"-goldie", ""},
		{"goldie-", ""},
		{"-", ""},
		{"goldie_3", ""},
		{"goldie 4", ""},
		{"goldie\x00", ""},
		{"g\u00f6ldie", ""},
		{"goldie\u200b", ""},
		{"\u212aoldie-5", ""}, // Kelvin sign: strings.ToLower makes it "koldie-5"
		{"\uff47oldie-6", ""}, // fullwidth g
	} {
		r := &fakeResolver{
			hosts: map[string][]string{headless: {"10.0.1.1"}},
			ptrs:  map[string][]string{"10.0.1.1": {c.label + ".goldie-headless.goldie.svc.cluster.local."}},
		}
		peers, skipped, err := lookupActivityPeers(t.Context(), r, headless, "8080")
		if err != nil {
			t.Fatalf("PTR label %q: lookupActivityPeers: %v", c.label, err)
		}
		hostRule := replicaNameFromHostname(c.label)
		if c.want == "" {
			if len(peers) != 0 || skipped != 1 {
				t.Errorf("PTR label %q: peers %+v skipped %d, want it refused and counted", c.label, peers, skipped)
			}
			if hostRule != "goldie" {
				t.Errorf("replicaNameFromHostname(%q) = %q, but the PTR rule refuses it: one rule must decide both", c.label, hostRule)
			}
			continue
		}
		want := []activityPeer{{Name: c.want, URL: "http://10.0.1.1:8080"}}
		if !slices.Equal(peers, want) || skipped != 0 {
			t.Errorf("PTR label %q: peers %+v skipped %d, want %+v", c.label, peers, skipped, want)
		}
		if hostRule != c.want {
			t.Errorf("replicaNameFromHostname(%q) = %q, but the PTR rule names the peer %q: one rule must decide both", c.label, hostRule, c.want)
		}
	}
}

// This host's own interface addresses are dropped, and they are not counted
// as skipped: this replica is no peer of itself, and no unnamed peer either.
func TestLookupActivityPeersDropsThisHostsOwnAddresses(t *testing.T) {
	own, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatalf("net.InterfaceAddrs: %v", err)
	}
	r := &fakeResolver{hosts: map[string][]string{}, ptrs: map[string][]string{}}
	n := 0
	for _, a := range own {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP.String()
		n++
		r.hosts[headless] = append(r.hosts[headless], ip)
		r.ptrs[ip] = []string{fmt.Sprintf("goldie-%d.goldie-headless.goldie.svc.cluster.local.", n)}
	}
	if n == 0 {
		t.Skip("this host reports no interface addresses")
	}
	const other = "192.0.2.10" // TEST-NET-1, never an interface of the test host
	r.hosts[headless] = append(r.hosts[headless], other)
	r.ptrs[other] = []string{"goldie-99.goldie-headless.goldie.svc.cluster.local."}

	peers, skipped, err := lookupActivityPeers(t.Context(), r, headless, "8080")
	if err != nil {
		t.Fatalf("lookupActivityPeers: %v", err)
	}
	want := []activityPeer{{Name: "goldie-99", URL: "http://192.0.2.10:8080"}}
	if !slices.Equal(peers, want) {
		t.Errorf("peers %+v, want only %+v: this host's own addresses must be dropped", peers, want)
	}
	if skipped != 0 {
		t.Errorf("skipped %d, want 0: this host's own addresses are not unnamed peers", skipped)
	}
}

// Peer addresses come only from operator configuration (security-reviewer,
// PR #378): the A answers for the configured name. The lookup asks DNS for
// that name and nothing else; an answer that is not an IP address (a name, a
// host:port, userinfo, a URL, empty, padded) never becomes a peer; and a PTR
// answer only names a peer: a PTR under the configured name followed by more
// labels sends nothing to that name, the URL is still the A address.
func TestLookupActivityPeersTakesAddressesOnlyFromTheConfiguredNamesAnswers(t *testing.T) {
	r := &fakeResolver{
		hosts: map[string][]string{headless: {
			"evil.example.com", "10.0.0.5:9999", "user@10.0.0.6", "http://10.0.0.7", "", "10.0.0.8 ", "10.0.0.9",
		}},
		ptrs: map[string][]string{
			"evil.example.com": {"goldie-1.goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.5:9999":    {"goldie-2.goldie-headless.goldie.svc.cluster.local."},
			"user@10.0.0.6":    {"goldie-3.goldie-headless.goldie.svc.cluster.local."},
			"http://10.0.0.7":  {"goldie-4.goldie-headless.goldie.svc.cluster.local."},
			"":                 {"goldie-5.goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.8 ":        {"goldie-6.goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.9":         {"goldie-7.goldie-headless.goldie.svc.evil.example."},
		},
	}
	peers, _, err := lookupActivityPeers(t.Context(), r, headless, "8080")
	if err != nil {
		t.Fatalf("lookupActivityPeers: %v", err)
	}
	want := []activityPeer{{Name: "goldie-7", URL: "http://10.0.0.9:8080"}}
	if !slices.Equal(peers, want) {
		t.Errorf("peers %+v, want only %+v: an A answer that is not an IP address is no peer, and the URL is never the PTR's name", peers, want)
	}
	if !slices.Equal(r.asked, []string{headless}) {
		t.Errorf("LookupHost asked %q, want only the configured name %q", r.asked, headless)
	}
	for _, a := range r.ptrAsked {
		if net.ParseIP(a) == nil {
			t.Errorf("LookupAddr asked %q, which is not an IP address", a)
		}
	}
}

// Every DNS query of the lookup runs under a deadline, even when the caller's
// context has none, so a slow resolver cannot hold a federated request.
func TestLookupActivityPeersRunsUnderADeadline(t *testing.T) {
	ctx := context.WithoutCancel(t.Context())
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("the test's context already has a deadline")
	}
	r := &fakeResolver{
		hosts: map[string][]string{headless: {"10.0.0.5", "10.0.0.6"}},
		ptrs: map[string][]string{
			"10.0.0.5": {"goldie-0.goldie-headless.goldie.svc.cluster.local."},
			"10.0.0.6": {"goldie-1.goldie-headless.goldie.svc.cluster.local."},
		},
	}
	if _, _, err := lookupActivityPeers(ctx, r, headless, "8080"); err != nil {
		t.Fatalf("lookupActivityPeers: %v", err)
	}
	if r.noDeadline != 0 {
		t.Errorf("%d of %d DNS queries ran without a deadline", r.noDeadline, len(r.asked)+len(r.ptrAsked))
	}
}

// The peers are capped: a name with thousands of addresses yields a bounded
// set of peers, and no PTR query goes out for an address past the cap. The
// cap's value is the implementation's; the record names none, but it is at
// least the 11 addresses TestLookupActivityPeersNamesPeersByTheirPTRLabel
// resolves.
func TestLookupActivityPeersCapsThePeers(t *testing.T) {
	const many = 4096
	r := &fakeResolver{hosts: map[string][]string{}, ptrs: map[string][]string{}}
	for i := range many {
		addr := fmt.Sprintf("10.1.%d.%d", i/256, i%256)
		r.hosts[headless] = append(r.hosts[headless], addr)
		r.ptrs[addr] = []string{fmt.Sprintf("goldie-%d.goldie-headless.goldie.svc.cluster.local.", i)}
	}
	peers, _, err := lookupActivityPeers(t.Context(), r, headless, "8080")
	if err != nil {
		t.Fatalf("lookupActivityPeers: %v", err)
	}
	if len(peers) == 0 || len(peers) >= many {
		t.Errorf("%d peers of %d named addresses, want some, capped", len(peers), many)
	}
	if len(r.ptrAsked) != len(peers) {
		t.Errorf("%d PTR queries for %d peers, all of them named: no PTR query past the cap", len(r.ptrAsked), len(peers))
	}
}

// Only global unicast addresses are peers; private ranges are. Loopback,
// link-local, multicast, unspecified and broadcast answers are dropped before
// any PTR query and are not counted as skipped: the forwarded token never
// goes to them (#398 review).
func TestLookupActivityPeersTakesGlobalUnicastAddressesOnly(t *testing.T) {
	refused := []string{"127.0.0.1", "::1", "169.254.169.254", "fe80::1", "224.0.0.1", "ff02::1", "0.0.0.0", "::", "255.255.255.255"}
	r := &fakeResolver{
		hosts: map[string][]string{headless: append(slices.Clone(refused), "10.0.2.5", "192.168.2.6", "fd00::2:7")},
		ptrs: map[string][]string{
			"10.0.2.5":    {"goldie-0.goldie-headless.goldie.svc.cluster.local."},
			"192.168.2.6": {"goldie-1.goldie-headless.goldie.svc.cluster.local."},
			"fd00::2:7":   {"goldie-2.goldie-headless.goldie.svc.cluster.local."},
		},
	}
	for i, a := range refused {
		r.ptrs[a] = []string{fmt.Sprintf("goldie-%d.goldie-headless.goldie.svc.cluster.local.", 10+i)}
	}
	peers, skipped, err := lookupActivityPeers(t.Context(), r, headless, "8080")
	if err != nil {
		t.Fatalf("lookupActivityPeers: %v", err)
	}
	want := []activityPeer{
		{Name: "goldie-0", URL: "http://10.0.2.5:8080"},
		{Name: "goldie-1", URL: "http://192.168.2.6:8080"},
		{Name: "goldie-2", URL: "http://[fd00::2:7]:8080"},
	}
	if !slices.Equal(peers, want) || skipped != 0 {
		t.Errorf("peers %+v skipped %d, want %+v and 0", peers, skipped, want)
	}
	for _, a := range r.ptrAsked {
		if slices.Contains(refused, a) {
			t.Errorf("a PTR query went out for %s", a)
		}
	}
}

// A name is one replica. This replica's own name, the "goldie" fallback
// included, is no peer and not counted. One IPv4 and one IPv6 address may
// share a name (a dual-stack pod); two addresses of one family claiming a name
// make it no peer, and every address claiming it counts as skipped (#398
// review).
func TestLookupActivityPeersKeepsNamesUnique(t *testing.T) {
	g0 := activityPeer{Name: "goldie-0", URL: "http://10.0.3.1:8080"}
	g1 := activityPeer{Name: "goldie-1", URL: "http://10.0.3.2:8080"}
	g := activityPeer{Name: "goldie", URL: "http://10.0.3.7:8080"}
	g4 := activityPeer{Name: "goldie-4", URL: "http://10.0.3.8:8080"}
	for self, want := range map[string][]activityPeer{"goldie-0": {g1, g, g4}, "goldie": {g0, g1, g4}} {
		old := activityReplicaName
		activityReplicaName = func() string { return self }
		r := &fakeResolver{
			hosts: map[string][]string{headless: {
				"10.0.3.1", "10.0.3.2", "fd00::3:2", "10.0.3.3", "10.0.3.4", "fd00::3:5", "fd00::3:6", "10.0.3.7", "10.0.3.8",
			}},
			ptrs: map[string][]string{
				"10.0.3.1":  {"goldie-0.goldie-headless.goldie.svc.cluster.local."}, // this replica, maybe
				"10.0.3.2":  {"goldie-1.goldie-headless.goldie.svc.cluster.local."}, // dual-stack goldie-1
				"fd00::3:2": {"goldie-1.goldie-headless.goldie.svc.cluster.local."},
				"10.0.3.3":  {"goldie-2.goldie-headless.goldie.svc.cluster.local."}, // two IPv4 claim goldie-2
				"10.0.3.4":  {"goldie-2.goldie-headless.goldie.svc.cluster.local."},
				"fd00::3:5": {"goldie-3.goldie-headless.goldie.svc.cluster.local."}, // two IPv6 claim goldie-3
				"fd00::3:6": {"goldie-3.goldie-headless.goldie.svc.cluster.local."},
				"10.0.3.7":  {"goldie.goldie-headless.goldie.svc.cluster.local."}, // the fallback name
				"10.0.3.8":  {"goldie-4.goldie-headless.goldie.svc.cluster.local."},
			},
		}
		peers, skipped, err := lookupActivityPeers(t.Context(), r, headless, "8080")
		activityReplicaName = old
		if err != nil {
			t.Fatalf("self %q: lookupActivityPeers: %v", self, err)
		}
		if !slices.Equal(peers, want) || skipped != 4 {
			t.Errorf("self %q: peers %+v skipped %d, want %+v and 4", self, peers, skipped, want)
		}
	}
}
