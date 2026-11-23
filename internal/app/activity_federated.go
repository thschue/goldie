package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// activityPeerIdle is the idle read deadline of a peer stream: with no byte,
// keepalives included, for this long, the peer shows as down. Read when the
// public handler is built.
var activityPeerIdle = 45 * time.Second

// resolveActivityPeers resolves the configured DNS name to the peers, this
// replica among them. Nil in production, where activityPeerFinder does it
// over the system resolver; a variable so tests stand in for DNS.
var resolveActivityPeers func(ctx context.Context, name string) ([]activityPeer, error)

// peerLookupShare is how long one lookup of the peers serves every merged
// stream and reconnect of this replica.
const peerLookupShare = 3 * time.Second

// peerError is a failure to open a peer. It carries only a reason class, never
// the cause, which may name an address.
type peerError struct{ reason string }

func (e *peerError) Error() string      { return "the peer's activity stream cannot be opened" }
func (e *peerError) DownReason() string { return e.reason }

// errPeerUnavailable is a failure of the resolver, which may name a DNS server.
var errPeerUnavailable = &peerError{"lookup_failed"}

// openError classes the failure of StreamActivity.
func openError(err error) error {
	var apiErr *contract.APIError
	switch {
	case errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden):
		return &peerError{"refused_token"}
	case errors.Is(err, contract.ErrRedirect):
		return &peerError{"redirect"}
	case errors.Is(err, contract.ErrNoLiveMarker):
		return &peerError{"no_live_marker"}
	}
	return &peerError{"open_failed"}
}

// activityFederation is the server option of a replica configured with
// GOLDIE_ACTIVITY_PEERS, none without. Peers come only from that DNS name,
// never a request; the caller's token goes only to the addresses it resolves
// to, over a client that follows no redirect and reads no proxy.
func activityFederation(cfg platform.Config, logger *slog.Logger) []api.ServerOption {
	if cfg.ActivityPeers == "" {
		return nil
	}
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || port == "" || port == "0" {
		port = "8080"
	}
	finder := &activityPeerFinder{resolver: net.DefaultResolver, logger: logger, name: cfg.ActivityPeers, port: port, ttl: peerLookupShare}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConnsPerHost:   2,
	}
	httpc := &http.Client{Transport: transport}
	idle := activityPeerIdle
	return []api.ServerOption{api.FederateActivity(api.Federation{
		Window: cfg.ActivityReorderWindow,
		Logger: logger,
		PeersFor: func() func(context.Context) ([]api.Peer, error) {
			find := finder.find
			if stub := resolveActivityPeers; stub != nil { // tests stand in for DNS; read here, on the request's goroutine
				find = func(ctx context.Context) ([]activityPeer, error) { return stub(ctx, cfg.ActivityPeers) }
			}
			return func(ctx context.Context) ([]api.Peer, error) {
				found, err := find(ctx)
				if err != nil {
					return nil, errPeerUnavailable // the resolver's error may name a DNS server
				}
				peers := make([]api.Peer, 0, len(found))
				for _, p := range found {
					peers = append(peers, api.Peer{Name: p.Name, Open: peerOpener(httpc, p.URL, idle)})
				}
				return peers, nil
			}
		},
	})}
}

// peerOpener opens the local activity stream of the peer at base with the
// caller's token: the plain stream, never replicas=all, so a cycle of
// replicas does not fan out.
func peerOpener(httpc *http.Client, base string, idle time.Duration) func(context.Context, string) ([]contract.ActivityEvent, <-chan contract.ActivityEvent, error) {
	return func(ctx context.Context, token string) ([]contract.ActivityEvent, <-chan contract.ActivityEvent, error) {
		client, err := contract.NewAPIClient(base, token, platform.Version, httpc)
		if err != nil {
			return nil, nil, &peerError{"open_failed"}
		}
		history, live, err := client.StreamActivity(ctx, idle)
		if err != nil {
			return nil, nil, openError(err)
		}
		return history, live, nil
	}
}
