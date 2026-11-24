package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/platform"
)

// These tests drive /mcp on the public handler with an MCP client over
// streamable HTTP (#106). The MCP server behind it reaches the API handler in
// process (ADR 0003, mode all) with the caller's bearer token.

// mcpEndpoint is where the public handler serves MCP over streamable HTTP.
const mcpEndpoint = "/mcp"

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// withBearer sends token (none when empty) on every request through next.
func withBearer(token string, next http.RoundTripper) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		return next.RoundTrip(r)
	})
}

// connectHTTP connects an MCP client over streamable HTTP to url through rt.
// The standalone SSE stream is off, so a round tripper without streaming
// support works; retries are off, so a rejection is reported at once.
func connectHTTP(t *testing.T, url string, rt http.RoundTripper) (*mcp.ClientSession, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{
			Endpoint:             url,
			HTTPClient:           &http.Client{Transport: rt},
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		}, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, nil
}

// mustConnectHTTP is connectHTTP that fails the test on error.
func mustConnectHTTP(t *testing.T, url string, rt http.RoundTripper) *mcp.ClientSession {
	t.Helper()
	cs, err := connectHTTP(t, url, rt)
	if err != nil {
		t.Fatalf("connect to %s over streamable HTTP: %v", mcpEndpoint, err)
	}
	return cs
}

// assertToolsAnswerOverHTTP lists the tools and calls both against the test
// content.
func assertToolsAnswerOverHTTP(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools over HTTP: %v", err)
	}
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = true
	}
	if len(got) != 11 || !got["find_decisions"] || !got["search_knowledge"] || !got["get_article"] || !got["list_projects"] || !got["current_context"] || !got["find_skills"] || !got["get_skill"] || !got["list_skills"] || !got["list_agents"] || !got["list_loops"] || !got["get_loop"] {
		t.Errorf("tools over HTTP = %v, want exactly search_knowledge, get_article, list_projects, current_context, find_skills, get_skill, list_skills, list_agents, list_loops, get_loop and find_decisions", got)
	}
	text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "agents loops"})
	if isErr || !strings.Contains(text, "agent-loops") || !strings.Contains(text, "Autonomous agent loops") {
		t.Errorf("search_knowledge over HTTP: error=%v, want the agent-loops hit:\n%s", isErr, text)
	}
	text, isErr = call(t, cs, "get_article", map[string]any{"id": "agent-loops"})
	if isErr || !strings.Contains(text, "Three loops turn issues into merged pull requests.") {
		t.Errorf("get_article over HTTP: error=%v, want the full article:\n%s", isErr, text)
	}
}

// Criterion: /mcp serves the MCP server over streamable HTTP, wired in
// internal/app: the public handler answers an MCP client with both tools.
func TestMCPOverHTTPIsServedByThePublicHandler(t *testing.T) {
	srv := newServer(t)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	assertToolsAnswerOverHTTP(t, cs)
}

// /mcp is there whether Swagger UI is on or off.
func TestMCPOverHTTPDoesNotDependOnSwaggerUI(t *testing.T) {
	srv := newServerWith(t, platform.Config{SwaggerUIEnabled: false})
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	assertToolsAnswerOverHTTP(t, cs)
}

// inProcess is a round tripper that hands each request straight to h: no
// listener, no address, no network. The request host does not resolve.
func inProcess(h http.Handler) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		resp := rec.Result()
		resp.Request = r
		return resp, nil
	})
}

// Criterion: the MCP server's client calls the API handler in process, not
// over the network. The public handler is never put on a listener here, so
// the tools can only answer if the API is reached without one.
func TestMCPOverHTTPCallsTheAPIInProcess(t *testing.T) {
	h, err := PublicHandler(platform.Config{ContentDir: testContent, APIToken: testToken, RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	cs := mustConnectHTTP(t, "http://goldie.invalid"+mcpEndpoint, withBearer(testToken, inProcess(h)))
	assertToolsAnswerOverHTTP(t, cs)
}

// A hostile caller cannot steer the API calls, and so the token, to another
// origin through the request's Host or forwarding headers: the tools answer
// from the in-process API and the other origin sees no request at all.
func TestMCPOverHTTPNeverSendsTheTokenToTheRequestHost(t *testing.T) {
	var hits atomic.Int64
	var leaked atomic.Bool
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.Contains(r.Header.Get("Authorization"), testToken) {
			leaked.Store(true)
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(attacker.Close)
	evil := strings.TrimPrefix(attacker.URL, "http://")
	srv := newServer(t)
	steer := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Host = evil
		r.Header.Set("X-Forwarded-Host", evil)
		r.Header.Set("X-Forwarded-Proto", "http")
		r.Header.Set("Forwarded", "host="+evil+";proto=http")
		return http.DefaultTransport.RoundTrip(r)
	})
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, steer))
	assertToolsAnswerOverHTTP(t, cs)
	if n := hits.Load(); n != 0 || leaked.Load() {
		t.Errorf("the origin named in Host and forwarding headers got %d requests (token leaked: %v), want none",
			n, leaked.Load())
	}
}

// initializeBody is an MCP initialize request, so a rejection is the auth's
// and not the protocol's.
const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
	`"capabilities":{},"clientInfo":{"name":"hostile","version":"v0"}}}`

// mcpRequest makes a request look like an MCP client's.
func mcpRequest(body string, auth func(*http.Request)) func(*http.Request) {
	return func(r *http.Request) {
		r.Body = io.NopCloser(strings.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if auth != nil {
			auth(r)
		}
	}
}

// Criterion: /mcp is rejected without a token. Every method the transport
// uses, with every malformed or wrong credential, is the 401 envelope of the
// API, which names the Bearer scheme and echoes no token.
func TestMCPOverHTTPRejectsMissingOrWrongToken(t *testing.T) {
	srv := newServer(t)
	const wrong = "wrong-token-presented-here-0123456789"
	auths := map[string]func(*http.Request){
		"no header":          nil,
		"wrong token":        bearer(wrong),
		"token prefix":       bearer(testToken[:len(testToken)-1]),
		"token suffix":       bearer(testToken + "x"),
		"empty bearer":       func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") },
		"scheme only":        func(r *http.Request) { r.Header.Set("Authorization", "Bearer") },
		"basic scheme":       func(r *http.Request) { r.Header.Set("Authorization", "Basic "+testToken) },
		"token without type": func(r *http.Request) { r.Header.Set("Authorization", testToken) },
		"token in query":     func(r *http.Request) { r.URL.RawQuery = "access_token=" + testToken },
		"token in cookie":    func(r *http.Request) { r.Header.Set("Cookie", "token="+testToken) },
	}
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		for name, auth := range auths {
			resp, body := do(t, method, srv.URL+mcpEndpoint, mcpRequest(initializeBody, auth))
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s, %s: status %d, want 401: %s", method, mcpEndpoint, name, resp.StatusCode, body)
				continue
			}
			if code, hint := errorHint(t, body); code != "unauthorized" || !strings.Contains(hint, "Bearer") {
				t.Errorf("%s, %s: code %q hint %q, want unauthorized with a bearer hint", method, name, code, hint)
			}
			if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("%s, %s: WWW-Authenticate %q, want Bearer", method, name, got)
			}
			if resp.Header.Get("Mcp-Session-Id") != "" {
				t.Errorf("%s, %s: a rejected caller was given a session id", method, name)
			}
			for _, secret := range []string{testToken, wrong, testToken[:len(testToken)-1]} {
				if strings.Contains(string(body), secret) {
					t.Errorf("%s, %s: the body echoes a token: %s", method, name, body)
				}
			}
		}
	}
}

// An MCP client without the token, or with a wrong one, cannot connect.
func TestMCPOverHTTPClientWithoutTheTokenCannotConnect(t *testing.T) {
	srv := newServer(t)
	for name, token := range map[string]string{"no token": "", "wrong token": "wrong-token-0123456789abcdef0123456789"} {
		if cs, err := connectHTTP(t, srv.URL+mcpEndpoint, withBearer(token, http.DefaultTransport)); err == nil {
			_ = cs.Close()
			t.Errorf("%s: the MCP client connected, want it rejected", name)
		}
	}
}

// recorded is the last tool call an MCP client sent: its headers, body and
// the session id the server gave it.
type recorded struct {
	mu      sync.Mutex
	header  http.Header
	body    []byte
	session string
}

// recordToolCalls records each tools/call request and the session id.
func recordToolCalls(rec *recorded, next http.RoundTripper) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		resp, err := next.RoundTrip(r)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if bytes.Contains(body, []byte(`"tools/call"`)) {
			rec.header, rec.body = r.Header.Clone(), body
		}
		if resp != nil && resp.Header.Get("Mcp-Session-Id") != "" {
			rec.session = resp.Header.Get("Mcp-Session-Id")
		}
		return resp, err
	})
}

// The token guards every request, not only the first: a caller who replays a
// legitimate client's tool call, session id and all, without the token or
// with a wrong one, gets the 401 and no tool result. The same replay with the
// token is answered, so the request itself is valid.
func TestMCPOverHTTPSessionNeedsTheTokenOnEveryRequest(t *testing.T) {
	srv := newServer(t)
	rec := &recorded{}
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, recordToolCalls(rec, withBearer(testToken, http.DefaultTransport)))
	if text, isErr := call(t, cs, "get_article", map[string]any{"id": "agent-loops"}); isErr {
		t.Fatalf("get_article over HTTP: %s", text)
	}
	rec.mu.Lock()
	header, body := rec.header.Clone(), rec.body
	rec.mu.Unlock()
	if body == nil {
		t.Fatal("no tools/call request was recorded")
	}
	replay := func(auth func(*http.Request)) func(*http.Request) {
		return func(r *http.Request) {
			r.Header = header.Clone()
			r.Header.Del("Authorization")
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			if auth != nil {
				auth(r)
			}
		}
	}
	const article = "Three loops turn issues into merged pull requests."
	for name, auth := range map[string]func(*http.Request){
		"no token":    nil,
		"wrong token": bearer("wrong-token-0123456789abcdef0123456789"),
	} {
		resp, got := do(t, http.MethodPost, srv.URL+mcpEndpoint, replay(auth))
		if resp.StatusCode != http.StatusUnauthorized || strings.Contains(string(got), article) {
			t.Errorf("replayed tool call, %s: status %d, want 401 without the article: %s", name, resp.StatusCode, got)
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+mcpEndpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	replay(bearer(testToken))(req)
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // test server URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(got), "Three loops turn issues") {
		t.Errorf("replayed tool call with the token: status %d, want 200 with the article: %s", resp.StatusCode, got)
	}
}

// A page on another origin is granted no cross-origin access to /mcp: neither
// the preflight nor a request with the token carries a CORS grant for it.
func TestMCPOverHTTPGrantsNoCrossOriginAccess(t *testing.T) {
	srv := newServer(t)
	const origin = "https://evil.example"
	send := func(method string, mutate func(*http.Request)) *http.Response {
		req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+mcpEndpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		mutate(req)
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // test server URL
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp
	}
	cases := map[string]*http.Response{
		"preflight": send(http.MethodOptions, func(r *http.Request) {
			r.Header.Set("Access-Control-Request-Method", http.MethodPost)
			r.Header.Set("Access-Control-Request-Headers", "authorization, content-type, mcp-session-id")
		}),
		"request with the token": send(http.MethodPost, mcpRequest(initializeBody, bearer(testToken))),
	}
	for name, resp := range cases {
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got == "*" || got == origin || got == "null" {
			t.Errorf("%s: Access-Control-Allow-Origin %q grants another origin", name, got)
		}
		if resp.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("%s: Access-Control-Allow-Credentials is set", name)
		}
	}
}

// A tool error over HTTP carries the API's message and hint and nothing of
// how the API was reached: no address, URL, host or token.
func TestMCPOverHTTPToolErrorsLeakNothing(t *testing.T) {
	srv := newServer(t)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	text, isErr := call(t, cs, "get_article", map[string]any{"id": "no-such-article"})
	if !isErr {
		t.Fatalf("want a tool error, got %s", text)
	}
	for _, want := range []string{"no such article", "Search for the article"} {
		if !strings.Contains(text, want) {
			t.Errorf("tool error lacks %q: %s", want, text)
		}
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	for _, bad := range []string{testToken, host, "127.0.0.1", "http://", "localhost", "/api/"} {
		if strings.Contains(text, bad) {
			t.Errorf("tool error leaks %q: %s", bad, text)
		}
	}
	for name, re := range leaks {
		if m := re.FindString(text); m != "" {
			t.Errorf("tool error contains a %s (%q): %s", name, m, text)
		}
	}
}
