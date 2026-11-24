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
// the form goldie/<semver>, where a leading v (a release tag's) is skipped.
func clientVersion(values []string) (platform.Semver, bool) {
	raw, ok := goldieClient(values)
	if !ok {
		return platform.Semver{}, false
	}
	v, err := platform.ParseSemver(strings.TrimPrefix(raw, "v"))
	return v, err == nil
}

// goldieClient is what follows goldie/ in exactly one X-Goldie-Client value of
// at most maxClientHeader bytes.
func goldieClient(values []string) (string, bool) {
	if len(values) != 1 || len(values[0]) > maxClientHeader {
		return "", false
	}
	return strings.CutPrefix(values[0], contract.ClientHeaderPrefix)
}

// Client kinds: the via attribute of goldie.skill.gets and Event.Via. viaUI is
// an event's only (a metric has no such series).
const (
	viaCLI   = "cli"
	viaMCP   = "mcp"
	viaUI    = "ui"
	viaOther = "other"
)

// knownVia is v when it is one of the documented surfaces, else viaOther. A
// peer's value is untrusted text and must not reach a client as sent.
func knownVia(v string) string {
	switch v {
	case viaCLI, viaMCP, viaUI:
		return v
	}
	return viaOther
}

// clientVia is the surface a request came through: viaMCP when it carries
// exactly one X-Goldie-Via of mcp, else viaCLI when it names a goldie client
// (goldie/<anything>, a dev build included), else viaOther: no header, several,
// an oversized or foreign one. It returns one of three constants, never a value
// derived from the request (PC-12).
func clientVia(h http.Header) string {
	if v := h.Values(contract.ViaHeader); len(v) == 1 && v[0] == contract.ViaMCP {
		return viaMCP
	}
	if _, ok := goldieClient(h.Values(contract.ClientHeader)); ok {
		return viaCLI
	}
	return viaOther
}
