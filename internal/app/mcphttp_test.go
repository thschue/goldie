package app

import (
	"net/http"
	"testing"
)

// /mcp opens no event stream: with the token, GET and DELETE are the 405
// envelope of the public mux, naming POST, and no session id is handed out.
func TestMCPOverHTTPOpensNoEventStream(t *testing.T) {
	srv := newServer(t)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		resp, body := do(t, method, srv.URL+mcpEndpoint, mcpRequest(initializeBody, bearer(testToken)))
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodPost {
			t.Errorf("%s /mcp: status %d Allow %q, want 405 with Allow POST: %s",
				method, resp.StatusCode, resp.Header.Get("Allow"), body)
		}
		if code, _ := errorHint(t, body); code != "method_not_allowed" {
			t.Errorf("%s /mcp: code %q, want method_not_allowed", method, code)
		}
		if resp.Header.Get("Mcp-Session-Id") != "" {
			t.Errorf("%s /mcp: a session id was handed out", method)
		}
	}
}

// Every named token, reader, operator and hashed reader alike, reaches the
// tools over /mcp; the shorthand token, configured nowhere here, does not.
func TestMCPOverHTTPAcceptsEveryNamedToken(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines()})
	for _, token := range []string{readerToken, operatorToken, hashedSecret} {
		assertToolsAnswerOverHTTP(t, mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(token, http.DefaultTransport)))
	}
	if cs, err := connectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport)); err == nil {
		_ = cs.Close()
		t.Error("an unconfigured token connected")
	}
}
