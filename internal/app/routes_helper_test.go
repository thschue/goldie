package app

import (
	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// publicRoutes is the route table of the router newPublicMuxKeepalive builds over
// provider, behind token as the GOLDIE_API_TOKEN shorthand and the default
// rate limit. It is a test helper, not production code (#186): a token that
// fails api.OperatorToken is an error, never an empty token set.
func publicRoutes(provider func() *engine.Snapshot, token string) ([]route, error) {
	tokens, err := api.OperatorToken(token)
	if err != nil {
		return nil, err
	}
	m, err := newPublicMuxKeepalive(provider, tokens, rateLimiter(platform.DefaultRateLimit()), nil, api.DefaultActivityKeepalive)
	if err != nil {
		return nil, err
	}
	return m.routes, nil
}
