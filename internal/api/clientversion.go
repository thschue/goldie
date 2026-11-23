package api

import (
	"net/http"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// maxClientHeader bounds the X-Goldie-Client value the gate parses. Longer
// values are not a goldie version, so they are let through unread.
const maxClientHeader = 256

// ClientVersionGate refuses goldie clients older than a minimum version
// (GOLDIE_MIN_CLIENT_VERSION, #102). A nil gate lets every request through.
type ClientVersionGate struct {
	min platform.Semver
}

// NewClientVersionGate refuses goldie clients older than minimum.
func NewClientVersionGate(minimum platform.Semver) *ClientVersionGate {
	return &ClientVersionGate{min: minimum}
}

// Require answers 426 in the error envelope when the request names a goldie
// version older than the minimum, and otherwise serves next. A request that
// does not name a goldie version, with no header, several, or one that is not
// goldie/<semver>, is let through: the header is a courtesy for goldie's own
// CLI, not a credential, so refusing what it cannot read would protect
// nothing and lock out other clients. It goes inside a Tokens.Require guard
// and outside RateLimiter.Limit, so a 401 reveals nothing of it and a refusal
// spends no bucket.
func (g *ClientVersionGate) Require(next http.Handler) http.Handler {
	if g == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := clientVersion(r.Header.Values(contract.ClientHeader)); ok && v.Less(g.min) {
			// The hint uses String, which leaves out build metadata: it takes no
			// part in precedence, and clients need not see what an operator
			// writes there.
			WriteError(w, http.StatusUpgradeRequired, "upgrade_required",
				"this goldie client is older than the server accepts",
				"Upgrade goldie to version "+g.min.String()+" or newer, then retry.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientVersion reads the version from exactly one X-Goldie-Client value of
// the form goldie/<semver>, where a leading v (a release tag's) is skipped. A
// trailing " mcp" (goldie/<semver> mcp, from goldie mcp) is not part of the version.
func clientVersion(values []string) (platform.Semver, bool) {
	raw, ok := goldieClient(values)
	if !ok {
		return platform.Semver{}, false
	}
	raw = strings.TrimSuffix(raw, " "+contract.ClientViaMCP)
	v, err := platform.ParseSemver(strings.TrimPrefix(raw, "v"))
	return v, err == nil
}

// Client kinds, the via attribute of goldie.skill.gets.
const (
	viaCLI   = "cli"
	viaMCP   = "mcp"
	viaOther = "other"
)

// clientVia is the kind of client the header names: viaMCP for
// goldie/<version> mcp, viaCLI for goldie/<version>, and viaOther for anything
// else (no header, several, an oversized or foreign one, any other token). The
// version is not parsed, so a dev build counts too. It returns one of three
// constants, never a value derived from the request (PC-12).
func clientVia(values []string) string {
	rest, ok := goldieClient(values)
	switch {
	case !ok:
		return viaOther
	case strings.HasSuffix(rest, " "+contract.ClientViaMCP) && !strings.ContainsRune(strings.TrimSuffix(rest, " "+contract.ClientViaMCP), ' '):
		return viaMCP
	case !strings.ContainsRune(rest, ' '):
		return viaCLI
	}
	return viaOther
}

// goldieClient is what follows goldie/ in exactly one header value of at most
// maxClientHeader bytes.
func goldieClient(values []string) (string, bool) {
	if len(values) != 1 || len(values[0]) > maxClientHeader {
		return "", false
	}
	return strings.CutPrefix(values[0], contract.ClientHeaderPrefix)
}
