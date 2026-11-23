package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// sourceUpstreamCheck is the readiness check of the upstream Source (PC-02).
const sourceUpstreamCheck = "source:upstream"

// Upstream is a central goldie server an embedded server combines the
// repository's .goldie with: its base is the Project's resolved view from
// there (GET /api/v1/projects/{id}/items), and the local Scope goes on top.
// The upstream's enforced Items cannot be shadowed locally. The zero value is
// no upstream.
type Upstream struct {
	Addr    string // GOLDIE_ADDR, the central server's base URL; "" is no upstream
	Token   string // GOLDIE_TOKEN, sent to the server at Addr only
	Project string // the Project whose view is fetched, host/org/repo
}

// upstreamError marks an error of the upstream itself: it could not be
// reached or what it sent is not a valid view. Local.Start lets the poll loop
// retry these (the server starts, not ready), and stops on any other, such as
// a local Item that shadows an enforced one.
type upstreamError struct{ err error }

func (e *upstreamError) Error() string { return e.err.Error() }
func (e *upstreamError) Unwrap() error { return e.err }

// upstreamSource tags every error of the upstream's Source as an upstreamError.
type upstreamSource struct{ engine.Source }

func (u upstreamSource) Load(ctx context.Context) (*engine.Snapshot, error) {
	snap, err := u.Source.Load(ctx)
	if err != nil {
		return nil, &upstreamError{err}
	}
	return snap, nil
}

func (u upstreamSource) Revision() string {
	if r, ok := u.Source.(engine.Revisioner); ok {
		return r.Revision()
	}
	return ""
}

// upstreamBase checks up and the repository's config.yaml, and returns the
// Source of the upstream's view. It connects to nothing. The token goes only
// to up.Addr, which is GOLDIE_ADDR: a config.yaml addr is checked against it
// and never used itself, so a cloned repository cannot make the token go to an
// address it picks. Errors name the setting to fix and never an address or the
// token.
func upstreamBase(up Upstream, goldieDir string) (engine.Source, error) {
	if up.Project == "" {
		return nil, errors.New("an upstream needs a Project to fetch the view of")
	}
	if err := checkUpstreamAddr(up.Addr); err != nil {
		return nil, err
	}
	if goldieDir != "" {
		cfgAddr, err := engine.LocalConfigAddr(goldieDir)
		if err != nil {
			return nil, err
		}
		if cfgAddr != "" && !sameOrigin(cfgAddr, up.Addr) {
			return nil, fmt.Errorf(".goldie/config.yaml: addr is not the origin of %s, so the token is not sent to it; set %s to use that server", platform.EnvAddr, platform.EnvAddr)
		}
	}
	client, err := contract.NewAPIClient(up.Addr, up.Token, platform.Version, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", platform.EnvAddr, err)
	}
	return upstreamSource{engine.NewViewSource(func(ctx context.Context) (engine.View, string, error) {
		return fetchView(ctx, client, up.Project)
	})}, nil
}

// checkUpstreamAddr requires an http(s) URL with a host and no credentials,
// and https unless the host is loopback, before any connection is made.
func checkUpstreamAddr(addr string) error {
	if err := contract.CheckBaseURL(addr); err != nil {
		return fmt.Errorf("%s: %w", platform.EnvAddr, err)
	}
	u, err := url.Parse(addr)
	if err != nil || u.User != nil {
		return fmt.Errorf("%s must not hold credentials", platform.EnvAddr)
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		return fmt.Errorf("%s: the upstream must use https unless its host is loopback", platform.EnvAddr)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// origin is scheme://host:port of an http(s) URL, lower-cased, with the
// default port filled in, or false when raw is not one.
func origin(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	switch {
	case scheme != "http" && scheme != "https":
		return "", false
	case port == "" && scheme == "http":
		port = "80"
	case port == "":
		port = "443"
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), true
}

// sameOrigin reports whether a and b are URLs of one origin. A path or a
// trailing slash does not make another origin.
func sameOrigin(a, b string) bool {
	oa, okA := origin(a)
	ob, okB := origin(b)
	return okA && okB && oa == ob
}

// fetchView asks the upstream for the Project's view through the contract
// client (no redirect, capped answer, 30 s timeout). The error says what went
// wrong and holds nothing of the answer, the address or the token. The
// revision is a short digest of the view, since the endpoint has none.
func fetchView(ctx context.Context, client *contract.ClientWithResponses, proj string) (engine.View, string, error) {
	raw, err := client.GetProjectItems(ctx, proj)
	if err != nil {
		switch {
		case errors.Is(err, contract.ErrRedirect):
			return engine.View{}, "", errors.New("the central server answered with a redirect, which is not followed")
		case errors.Is(err, contract.ErrTooLarge):
			return engine.View{}, "", errors.New("the central server's answer is too large")
		case ctx.Err() != nil:
			return engine.View{}, "", ctx.Err()
		}
		return engine.View{}, "", errors.New("the central server cannot be reached")
	}
	resp, apiErr, err := contract.ParseOK(raw, contract.ParseGetProjectItemsResponse)
	switch {
	case errors.Is(err, contract.ErrTooLarge):
		return engine.View{}, "", errors.New("the central server's answer is too large")
	case err != nil || (apiErr == nil && resp.JSON200 == nil):
		return engine.View{}, "", errors.New("the central server's answer is not a Project view")
	case apiErr != nil && (apiErr.Status == 401 || apiErr.Status == 403):
		return engine.View{}, "", fmt.Errorf("the central server refused the token (HTTP %d); check GOLDIE_TOKEN", apiErr.Status)
	case apiErr != nil:
		return engine.View{}, "", fmt.Errorf("the central server answered HTTP %d", apiErr.Status)
	}
	view := engine.View{Suppressions: resp.JSON200.Suppressions}
	for _, it := range resp.JSON200.Items {
		a := engine.Article{
			ID: it.Id, Scope: it.Scope, Name: it.Name, Keywords: it.Keywords,
			Content: it.Content, Status: string(it.Status), Enforced: it.Enforced,
		}
		if it.ReplacedBy != nil {
			a.ReplacedBy = *it.ReplacedBy
		}
		view.Items = append(view.Items, a)
	}
	sum, _ := json.Marshal(view.Items)
	digest := sha256.Sum256(sum)
	return view, hex.EncodeToString(digest[:]), nil
}
