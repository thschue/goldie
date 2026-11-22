package app

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/platform"
)

// goldieClientHeader names the goldie client and its version as
// goldie/<version>. The CLI sends it on every request (#150).
const goldieClientHeader = "X-Goldie-Client"

// maxGoldieClientHeader bounds the header value the gate parses. Longer
// values are not a goldie version, so they are let through unread.
const maxGoldieClientHeader = 256

// clientGate refuses goldie clients older than GOLDIE_MIN_CLIENT_VERSION
// (#102). A nil gate lets every request through.
type clientGate struct {
	min     platform.Semver
	minText string // the setting as written, for the hint
}

// newClientGate builds the gate from GOLDIE_MIN_CLIENT_VERSION; "" is no gate.
// Errors name the variable and never contain the value.
func newClientGate(minimum string) (*clientGate, error) {
	if minimum == "" {
		return nil, nil
	}
	v, err := platform.ParseSemver(minimum)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", platform.EnvMinClientVersion, err)
	}
	return &clientGate{min: v, minText: minimum}, nil
}

// require answers 426 in the error envelope when the request names a goldie
// version older than the minimum, and otherwise serves next. A request that
// does not name a goldie version, with no header, several, or one that is not
// goldie/<semver>, is let through: the header is a courtesy for goldie's own
// CLI, not a credential, so refusing what it cannot read would protect
// nothing and lock out other clients. It goes inside the token check and
// outside the rate limit, so a 401 reveals nothing of it and a refusal spends
// no bucket.
func (g *clientGate) require(next http.Handler) http.Handler {
	if g == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := clientVersion(r.Header.Values(goldieClientHeader)); ok && v.Less(g.min) {
			api.WriteError(w, http.StatusUpgradeRequired, "upgrade_required",
				"this goldie client is older than the server accepts",
				"Upgrade goldie to version "+g.minText+" or newer, then retry.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientVersion reads the version from exactly one X-Goldie-Client value of
// the form goldie/<semver>, where a leading v (a release tag's) is skipped.
func clientVersion(values []string) (platform.Semver, bool) {
	if len(values) != 1 || len(values[0]) > maxGoldieClientHeader {
		return platform.Semver{}, false
	}
	raw, ok := strings.CutPrefix(values[0], "goldie/")
	if !ok {
		return platform.Semver{}, false
	}
	v, err := platform.ParseSemver(strings.TrimPrefix(raw, "v"))
	return v, err == nil
}
