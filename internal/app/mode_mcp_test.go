package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/platform"
)

// #637: GOLDIE_MODE=mcp serves /mcp over HTTP and nothing else, and reaches
// the API of GOLDIE_UPSTREAM_ADDR with each caller's own Authorization (ADR
// 0003). It holds no token. The settings matrix is TestMCPModeStartupValidation
// in internal/platform.

// mcpModeHandler is the public handler in mode mcp in front of upstream.
func mcpModeHandler(t *testing.T, upstream string) *Public {
	t.Helper()
	cfg := loadedConfig(t, map[string]string{envMode: "mcp", envUpstreamAddr: upstream})
	pub, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("PublicHandler in mode mcp: %v", err)
	}
	return pub
}

// mcpModeServer serves mcpModeHandler on a loopback address.
func mcpModeServer(t *testing.T, upstream string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(mcpModeHandler(t, upstream))
	t.Cleanup(srv.Close)
	return srv
}

// twoCallerUpstream is a real goldie server (mode all) that knows an operator
// and a reader token.
func twoCallerUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := sessionConfig(t, "ci-operator:operator:"+operatorToken+"\nci-reader:reader:"+readerToken, nil)
	h, err := PublicHandler(cfg, nil)
	if err != nil {
		t.Fatalf("upstream PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// fixedUpstream answers every request with status and a JSON error envelope.
func fixedUpstream(t *testing.T, status int, code string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","message":"upstream says no","details":{}}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// authRecorder is a proxy to a target that records the Authorization header
// of every request it forwards.
type authRecorder struct {
	*httptest.Server
	mu   sync.Mutex
	auth []string
}

func newAuthRecorder(t *testing.T, target string) *authRecorder {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rec := &authRecorder{}
	proxy := httputil.NewSingleHostReverseProxy(u)
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.auth = append(rec.auth, r.Header.Get("Authorization"))
		rec.mu.Unlock()
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(rec.Close)
	return rec
}

func (a *authRecorder) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.auth...)
}

func (a *authRecorder) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.auth = nil
}

// callWithin is a tool call that fails the test if it does not answer in d.
func callWithin(t *testing.T, cs *mcp.ClientSession, d time.Duration, tool string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s within %s: %v", tool, d, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// The MCP protocol (initialize, tools, search, lookup) works against mode mcp
// over a real API server, and /mcp is the only thing mounted.
func TestModeMCPServesMCPAndNothingElse(t *testing.T) {
	upstream := uiUpstream(t)
	srv := mcpModeServer(t, upstream.URL)

	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(operatorToken, http.DefaultTransport))
	assertToolsAnswerOverHTTP(t, cs)

	for _, p := range []string{"/", "/api/v1/search?q=kubernetes", "/api/v1/session", "/openapi.json", "/openapi.yaml", "/swagger-ui"} {
		if resp, _ := uiDo(t, http.MethodGet, upstream.URL+p, bearer(operatorToken)); resp.StatusCode != http.StatusOK {
			t.Fatalf("fixture: upstream GET %s: status %d", p, resp.StatusCode)
		}
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, bearer(operatorToken))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s in mode mcp: status %d, want 404", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "not_found")
	}
}

// Each tool call reaches the upstream with the caller's own Authorization:
// the server holds no token, sends none of its own and mixes no callers up.
func TestModeMCPForwardsEachCallersOwnAuthorization(t *testing.T) {
	rec := newAuthRecorder(t, twoCallerUpstream(t).URL)
	srv := mcpModeServer(t, rec.URL)

	for _, token := range []string{readerToken, operatorToken} {
		rec.reset()
		cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(token, http.DefaultTransport))
		text, isErr := callWithin(t, cs, 15*time.Second, "search_knowledge", map[string]any{"query": "agents loops"})
		if isErr || !strings.Contains(text, "agent-loops") {
			t.Fatalf("search_knowledge as %q: error=%v, want the agent-loops hit:\n%s", token[:6], isErr, text)
		}
		if text, isErr = callWithin(t, cs, 15*time.Second, "get_article", map[string]any{"id": "agent-loops"}); isErr {
			t.Fatalf("get_article as %q: tool error:\n%s", token[:6], text)
		}
		got := rec.seen()
		if len(got) < 2 {
			t.Fatalf("the upstream saw %d requests for two tool calls, want at least 2", len(got))
		}
		for _, a := range got {
			if a != "Bearer "+token {
				t.Errorf("the upstream saw Authorization %q for the caller %q, want exactly the caller's", a, token[:6])
			}
		}
	}
}

// A caller without a token gets no more than the upstream gives it: its tool
// calls are tool errors, and the server adds no credential of its own.
func TestModeMCPWithoutATokenSendsNoneUpstream(t *testing.T) {
	rec := newAuthRecorder(t, twoCallerUpstream(t).URL)
	srv := mcpModeServer(t, rec.URL)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer("", http.DefaultTransport))
	text, isErr := callWithin(t, cs, 15*time.Second, "search_knowledge", map[string]any{"query": "agents loops"})
	if !isErr {
		t.Errorf("search_knowledge without a token: not a tool error:\n%s", text)
	}
	for _, a := range rec.seen() {
		if a != "" {
			t.Errorf("the upstream saw Authorization %q for a caller without a token, want none", a)
		}
	}
}

// A caller cannot steer the upstream calls, and so its token, to another
// origin through Host or forwarding headers.
func TestModeMCPNeverSendsTheTokenToTheRequestHost(t *testing.T) {
	var mu sync.Mutex
	var hits int
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(attacker.Close)
	evil := strings.TrimPrefix(attacker.URL, "http://")
	srv := mcpModeServer(t, uiUpstream(t).URL)
	steer := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Host = evil
		r.Header.Set("X-Forwarded-Host", evil)
		r.Header.Set("Forwarded", "host="+evil+";proto=http")
		return http.DefaultTransport.RoundTrip(r)
	})
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(operatorToken, steer))
	assertToolsAnswerOverHTTP(t, cs)
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Errorf("the origin named in Host and forwarding headers got %d requests, want none", hits)
	}
}

// An upstream 401 or 503, and an unreachable upstream, reach the agent as a
// tool error that comes back at once, and say nothing of the upstream's
// address or the caller's token.
func TestModeMCPUpstreamFailuresAreToolErrors(t *testing.T) {
	const token = "caller-token-0123456789abcdef0123456789"
	dead := deadAddr(t)
	cases := []struct {
		name string
		addr string
	}{
		{"401", fixedUpstream(t, http.StatusUnauthorized, "unauthorized").URL},
		{"503", fixedUpstream(t, http.StatusServiceUnavailable, "unavailable").URL},
		{"unreachable", dead},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, _ := url.Parse(c.addr)
			srv := mcpModeServer(t, c.addr)
			cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(token, http.DefaultTransport))
			for tool, args := range map[string]map[string]any{
				"search_knowledge": {"query": "agents loops"},
				"get_article":      {"id": "agent-loops"},
				"list_projects":    {},
			} {
				text, isErr := callWithin(t, cs, 8*time.Second, tool, args)
				if !isErr {
					t.Errorf("%s with the upstream %s: not a tool error:\n%s", tool, c.name, text)
				}
				if text == "" {
					t.Errorf("%s with the upstream %s: the tool error says nothing", tool, c.name)
				}
				for _, leak := range []string{c.addr, u.Host, u.Port(), token, "refused", "dial"} {
					if strings.Contains(text, leak) {
						t.Errorf("%s with the upstream %s: the tool error contains %q:\n%s", tool, c.name, leak, text)
					}
				}
			}
		})
	}
}

// /readyz has the one upstream check, as in mode ui (#636).
func TestModeMCPReadyzHasOneUpstreamCheck(t *testing.T) {
	t.Run("an unauthenticated 401 envelope counts as reachable", func(t *testing.T) {
		code, raw, checks := uiReadyz(t, mcpModeHandler(t, uiUpstream(t).URL))
		if code != http.StatusOK || len(checks) != 1 || checks[0] != (readyzCheck{Name: "upstream", Status: "ok"}) {
			t.Errorf("/readyz = %d %s, want 200 with exactly [upstream ok]", code, raw)
		}
	})
	t.Run("an unreachable upstream fails the check without its address", func(t *testing.T) {
		dead := deadAddr(t)
		u, _ := url.Parse(dead)
		code, raw, checks := uiReadyz(t, mcpModeHandler(t, dead))
		if code != http.StatusServiceUnavailable || len(checks) != 1 || checks[0].Name != "upstream" || checks[0].Status == "ok" {
			t.Errorf("/readyz = %d %s, want 503 with exactly one failing upstream check", code, raw)
		}
		for _, leak := range []string{dead, u.Host, "refused"} {
			if strings.Contains(raw, leak) {
				t.Errorf("/readyz body contains %q: %s", leak, raw)
			}
		}
	})
}

// A hostile or missing upstream address is a startup error that names the
// variable and never the value. Which stage refuses it is not pinned.
func TestModeMCPRefusesAnUnsafeOrMissingUpstreamAddr(t *testing.T) {
	for name, addr := range map[string]string{
		"missing":     "",
		"credentials": "http://user:hunter2@127.0.0.1:9",
		"scheme":      "ftp://hunter2.example.com",
		"plain http":  "http://hunter2.example.com",
	} {
		env := map[string]string{envMode: "mcp"}
		if addr != "" {
			env[envUpstreamAddr] = addr
		}
		cfg, err := platform.LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
		if err == nil {
			_, err = PublicHandler(cfg, nil)
		}
		if err == nil {
			t.Errorf("%s: want a startup error", name)
			continue
		}
		if !strings.Contains(err.Error(), envUpstreamAddr) || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: error %q, want it to name %s and not the value", name, err, envUpstreamAddr)
		}
	}
}
