package app

import (
	"io"
	"net/http"
	"testing"
)

// mcpStatus posts an MCP initialize to /mcp with token and returns the status,
// without checking the answer against the OpenAPI document, which leaves /mcp
// out.
func mcpStatus(t *testing.T, url, token string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url+mcpEndpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	mcpRequest(initializeBody, bearer(token))(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// /mcp sits behind the same token guard as /api/, so the per-token limit
// covers it (#100: "and on /mcp once it exists"): a token over its limit gets
// the 429 envelope with Retry-After on /mcp too, counted under its name, while
// another token still connects.
func TestMCPOverHTTPIsRateLimitedPerToken(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1/m")})
	h := srv.Config.Handler
	if got := mcpStatus(t, srv.URL, readerToken); got != http.StatusOK {
		t.Fatalf("first /mcp request: status %d, want 200", got)
	}
	resp, body := do(t, http.MethodPost, srv.URL+mcpEndpoint, mcpRequest(initializeBody, bearer(readerToken)))
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "60" {
		t.Fatalf("/mcp over the limit: status %d Retry-After %q, want 429 and 60: %s", resp.StatusCode, resp.Header.Get("Retry-After"), body)
	}
	if code, _ := errorHint(t, body); code != "rate_limited" {
		t.Errorf("/mcp over the limit: code %q, want rate_limited", code)
	}
	if got := rejections(t, h)["ci-reader"]; got != 1 {
		t.Errorf("rejections = %d, want 1", got)
	}
	if got := mcpStatus(t, srv.URL, operatorToken); got != http.StatusOK {
		t.Errorf("another token on /mcp: status %d, want 200", got)
	}
}
