package cli

import (
	"context"
	"net/http"

	"github.com/thschue/goldie/internal/contract"
)

// tokenState is what the API document says about the token that was sent.
type tokenState int

const (
	// tokenUnset: no probe ran. The zero value, so a serverProbe nobody filled
	// in never reads as an answer.
	tokenUnset tokenState = iota
	// tokenUnchecked: the document answered, so the server is up, but it does
	// not say whether the token is good for the rest of the API.
	tokenUnchecked
	// tokenMissing: the server wants a token and none was given.
	tokenMissing
	// tokenRejected: the server refused the token that was given.
	tokenRejected
)

// serverProbe is what one read of the server's API document says. status and
// version both print it, so a server state gets the same words in both. title
// and apiVersion are empty unless the document answered. err is classified by
// contract.Answer and not yet worded: status words it with wordError, and
// version with its note and hint.
type serverProbe struct {
	title, apiVersion string
	tokenState        tokenState
	err               error
}

// answered reports whether the server returned its API document.
func (p serverProbe) answered() bool { return p.err == nil && p.tokenState == tokenUnchecked }

// probeServer reads the API document once. given says whether cl sends a
// token, which is how a 401 is told "no token" from "token rejected". A 401
// is not an error: the server is reachable, and the API name stays unknown.
//
// TODO(#6): when the status route exists, read it here, and status and version
// follow.
func probeServer(ctx context.Context, cl *contract.ClientWithResponses, given bool) serverProbe {
	raw, err := cl.GetOpenAPIJSON(ctx)
	if err == nil && raw.StatusCode == http.StatusUnauthorized {
		_ = raw.Body.Close()
		if given {
			return serverProbe{tokenState: tokenRejected}
		}
		return serverProbe{tokenState: tokenMissing}
	}
	spec, err := contract.Answer(raw, err, contract.ParseGetOpenAPIJSONResponse,
		func(r *contract.GetOpenAPIJSONResponse) *map[string]any { return r.JSON200 })
	if err != nil {
		return serverProbe{tokenState: tokenUnset, err: err}
	}
	info, _ := (*spec)["info"].(map[string]any)
	title, _ := info["title"].(string)
	apiVersion, _ := info["version"].(string)
	return serverProbe{title: title, apiVersion: apiVersion, tokenState: tokenUnchecked}
}
