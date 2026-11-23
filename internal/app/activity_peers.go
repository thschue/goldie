package app

import (
	"context"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

// activityPeer is one peer GOLDIE_ACTIVITY_PEERS resolves to: Name is its
// replica name, the only thing clients see of it; URL is the base URL the
// forwarded credential goes to, never shown to a client.
type activityPeer struct{ Name, URL string }

// peerResolver is what lookupActivityPeers needs of DNS; *net.Resolver
// satisfies it.
type peerResolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

const (
	// peerLookupTimeout bounds one lookup of the peers, the A query and every
	// PTR query together, whatever deadline the caller's context has.
	peerLookupTimeout = 3 * time.Second

	// maxActivityPeers caps the addresses one lookup sends PTR queries for,
	// and so the peers it returns.
	maxActivityPeers = 64
)

// lookupActivityPeers resolves name, the operator's GOLDIE_ACTIVITY_PEERS and
// never request input, to the peers. Each address of name that is an IP
// address and not one of this host's own gives a peer named by the first PTR
// answer of the form <label>.<name's labels>[.more] whose label passes
// replicaLabel and is not a dashed IP (10-1-2-3, CoreDNS's name for an
// endpoint without a hostname): activityPeer{label lower-cased,
// http://<address>:<port>}. A PTR answer only names a peer; the URL is always
// the A address. An address without such an answer is skipped and counted,
// never named after its address. Answers that are not IP addresses, repeated
// addresses, this host's own addresses and a second address of a name
// already found (a dual-stack pod) are dropped and not counted; past
// maxActivityPeers addresses no PTR query goes out. err is the A query's
// failure only; it may name the DNS server, so log it, never send it to a
// client.
func lookupActivityPeers(ctx context.Context, r peerResolver, name, port string) (peers []activityPeer, skipped int, err error) {
	ctx, cancel := context.WithTimeout(ctx, peerLookupTimeout)
	defer cancel()
	answers, err := r.LookupHost(ctx, name)
	if err != nil {
		return nil, 0, err
	}
	own := ownAddresses()
	addrs, names := map[string]bool{}, map[string]bool{}
	for _, a := range answers {
		ip := net.ParseIP(a)
		if ip == nil || addrs[ip.String()] || slices.ContainsFunc(own, ip.Equal) {
			continue
		}
		if len(addrs) == maxActivityPeers {
			break
		}
		addr := ip.String()
		addrs[addr] = true
		ptrs, _ := r.LookupAddr(ctx, addr)
		label, ok := peerName(ptrs, name)
		switch {
		case !ok:
			skipped++
		case !names[label]:
			names[label] = true
			peers = append(peers, activityPeer{Name: label, URL: "http://" + net.JoinHostPort(addr, port)})
		}
	}
	return peers, skipped, nil
}

// peerName is the replica name in the first of ptrs of the form
// <label>.<name's labels>[.more], compared without regard to ASCII case,
// whose label passes replicaLabel and is not a dashed IP.
func peerName(ptrs []string, name string) (string, bool) {
	under := lowerASCII(strings.TrimSuffix(name, ".")) + "."
	for _, ptr := range ptrs {
		label, rest, ok := strings.Cut(strings.TrimSuffix(ptr, "."), ".")
		if !ok || !strings.HasPrefix(lowerASCII(rest)+".", under) {
			continue
		}
		if n, ok := replicaLabel(label); ok && !dashedIP(n) {
			return n, true
		}
	}
	return "", false
}

// dashedIP reports whether label is an IP address with its dots or colons
// written as hyphens.
func dashedIP(label string) bool {
	return net.ParseIP(strings.ReplaceAll(label, "-", ".")) != nil ||
		net.ParseIP(strings.ReplaceAll(label, "-", ":")) != nil
}

// lowerASCII lower-cases the ASCII letters of s and nothing else.
func lowerASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// ownAddresses is this host's interface addresses: this replica is no peer of
// itself. None when they cannot be read.
func ownAddresses() []net.IP {
	ifaddrs, _ := net.InterfaceAddrs()
	var own []net.IP
	for _, a := range ifaddrs {
		if n, ok := a.(*net.IPNet); ok {
			own = append(own, n.IP)
		}
	}
	return own
}

// activityPeerFinder is lookupActivityPeers as the server runs it: one per
// process, for the configured name and this server's listen port. Peers
// without a pod name are logged once per finder, so once per process, as a
// count only: never an address or a PTR name.
type activityPeerFinder struct {
	resolver   peerResolver
	logger     *slog.Logger
	name, port string
	warn       sync.Once
}

// find resolves the peers; err is lookupActivityPeers'.
func (f *activityPeerFinder) find(ctx context.Context) ([]activityPeer, error) {
	peers, skipped, err := lookupActivityPeers(ctx, f.resolver, f.name, f.port)
	if skipped > 0 {
		f.warn.Do(func() {
			f.logger.Warn("activity peers without a pod name are skipped: run goldie as a StatefulSet whose serviceName is the headless Service", "skipped", skipped)
		})
	}
	return peers, err
}
