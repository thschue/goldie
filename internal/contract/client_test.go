package contract_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #182: contract owns the hardened API client that
// every goldie client (the CLI and goldie mcp over stdio) builds, and the one
// helper that lets only a 200 reach the generated parser. The API they pin:
//
//	func NewAPIClient(baseURL, token, version string, httpc *http.Client) (*ClientWithResponses, error)
//	var ErrRedirect, ErrTooLarge error
//	func ParseOK[T any](raw *http.Response, parse func(*http.Response) (T, error)) (T, *APIError, error)
//
// The version is a parameter because contract may not import platform. An
// empty token sends no Authorization header (status and version answer
// without one); httpc nil means a client of contract's own.

// maxAnswer is the cap on every answer body the hardened client reads.
const maxAnswer = 16 << 20

const clientToken = "contract-client-token-0123456789abcdef"

// recorder answers the search and article operations and records the
// Authorization and X-Goldie-Client headers of every request it receives.
type recorder struct {
	mu   sync.Mutex
	seen []http.Header
	srv  *httptest.Server
}

func (rc *recorder) requests() []http.Header {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]http.Header(nil), rc.seen...)
}

func (rc *recorder) reset() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.seen = nil
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	rc := &recorder{}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.mu.Lock()
		rc.seen = append(rc.seen, r.Header.Clone())
		rc.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Has("q") {
			_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x","name":"X","keywords":["k"],"content":"body"}`))
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

// calls are the two operations the clients make, each one request.
var calls = map[string]func(context.Context, *contract.ClientWithResponses) (int, error){
	"search": func(ctx context.Context, c *contract.ClientWithResponses) (int, error) {
		resp, err := c.SearchArticlesWithResponse(ctx, &contract.SearchArticlesParams{Q: "agents"})
		if err != nil {
			return 0, err
		}
		return resp.StatusCode(), nil
	},
	"article": func(ctx context.Context, c *contract.ClientWithResponses) (int, error) {
		resp, err := c.GetArticleWithResponse(ctx, "platform/deploy", nil)
		if err != nil {
			return 0, err
		}
		return resp.StatusCode(), nil
	},
}

func newAPIClient(t *testing.T, base, token string, httpc *http.Client) *contract.ClientWithResponses {
	t.Helper()
	c, err := contract.NewAPIClient(base, token, "1.4.0", httpc)
	if err != nil {
		t.Fatalf("NewAPIClient(%q): %v", base, err)
	}
	return c
}

// Every request carries the token as a bearer token and names the client and
// the version it was given in X-Goldie-Client, as goldie/<version>.
func TestAPIClientSendsTheTokenAndTheClientHeader(t *testing.T) {
	rc := newRecorder(t)
	for _, injected := range []*http.Client{nil, {}} {
		c := newAPIClient(t, rc.srv.URL, clientToken, injected)
		for name, call := range calls {
			rc.reset()
			status, err := call(context.Background(), c)
			if err != nil || status != http.StatusOK {
				t.Fatalf("%s (injected client %v): status %d, err %v", name, injected != nil, status, err)
			}
			got := rc.requests()
			if len(got) != 1 {
				t.Fatalf("%s: server saw %d requests, want 1", name, len(got))
			}
			if a := got[0].Get("Authorization"); a != "Bearer "+clientToken {
				t.Errorf("%s: Authorization = %q, want the bearer token", name, a)
			}
			if v := got[0].Values(contract.ClientHeader); len(v) != 1 || v[0] != contract.ClientHeaderPrefix+"1.4.0" {
				t.Errorf("%s: %s = %q, want exactly [%q]", name, contract.ClientHeader, v, contract.ClientHeaderPrefix+"1.4.0")
			}
		}
	}
}

// Without a token the client sends no Authorization header at all, not an
// empty bearer token, and still names itself.
func TestAPIClientWithoutATokenSendsNoAuthorization(t *testing.T) {
	rc := newRecorder(t)
	c := newAPIClient(t, rc.srv.URL, "", nil)
	if status, err := calls["search"](context.Background(), c); err != nil || status != http.StatusOK {
		t.Fatalf("search: status %d, err %v", status, err)
	}
	got := rc.requests()
	if len(got) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(got))
	}
	if v, ok := got[0]["Authorization"]; ok {
		t.Errorf("Authorization sent without a token: %q", v)
	}
	if v := got[0].Get(contract.ClientHeader); v != contract.ClientHeaderPrefix+"1.4.0" {
		t.Errorf("%s = %q, want %q", contract.ClientHeader, v, contract.ClientHeaderPrefix+"1.4.0")
	}
}

// redirectTo answers every request with code and a Location on target, under
// a path that marks a leak.
func redirectTo(t *testing.T, code int, target string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target+"/leak-marker-location"+r.URL.RequestURI())
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// No redirect is ever followed, whatever the injected client's own policy:
// each of 301, 302, 307 and 308 is ErrRedirect, the target never sees a
// request (so never the token), and the error carries nothing of the answer.
// The target is the same host on another port, where Go's own redirect
// policy forwards Authorization; the guard proves the harness sees a leak.
func TestAPIClientRefusesEveryRedirectAndNeverSendsTheTokenOn(t *testing.T) {
	target := newRecorder(t)
	following := &http.Client{} // follows redirects, as any http.Client does

	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		from := redirectTo(t, code, target.srv.URL)

		// Guard: the injected client alone carries the token to the target.
		req, _ := http.NewRequest(http.MethodGet, from.URL+"/api/v1/search?q=guard", nil)
		req.Header.Set("Authorization", "Bearer "+clientToken)
		resp, err := following.Do(req)
		if err != nil {
			t.Fatalf("guard %d: %v", code, err)
		}
		_ = resp.Body.Close()
		if got := target.requests(); len(got) != 1 || got[0].Get("Authorization") != "Bearer "+clientToken {
			t.Fatalf("guard %d: target saw %v; the harness cannot detect a forwarded token", code, got)
		}
		target.reset()

		for clientName, injected := range map[string]*http.Client{"own client": nil, "injected following client": following} {
			c := newAPIClient(t, from.URL, clientToken, injected)
			for name, call := range calls {
				t.Run(http.StatusText(code)+"/"+clientName+"/"+name, func(t *testing.T) {
					defer target.reset()
					status, err := call(context.Background(), c)
					if got := target.requests(); len(got) != 0 {
						t.Errorf("the client followed the %d: the target saw %d requests, Authorization %q", code, len(got), got[0].Get("Authorization"))
					}
					if !errors.Is(err, contract.ErrRedirect) {
						t.Fatalf("a %d answer: status %d, err %v, want ErrRedirect", code, status, err)
					}
					for _, bad := range []string{clientToken, "leak-marker", target.srv.URL, strings.TrimPrefix(target.srv.URL, "http://")} {
						if strings.Contains(err.Error(), bad) {
							t.Errorf("ErrRedirect error %q carries %q", err, bad)
						}
					}
				})
			}
		}
	}
}

// The constructor hardens a copy: the caller's client keeps its own redirect
// policy and transport.
func TestAPIClientLeavesTheInjectedClientAsItWas(t *testing.T) {
	tr := &http.Transport{}
	injected := &http.Client{Transport: tr}
	newAPIClient(t, "http://127.0.0.1:1", clientToken, injected)
	if injected.CheckRedirect != nil || injected.Transport != tr {
		t.Errorf("NewAPIClient changed the injected client: CheckRedirect set %v, transport replaced %v",
			injected.CheckRedirect != nil, injected.Transport != tr)
	}
}

// sized answers every request with status and a JSON search answer padded
// with spaces to exactly n bytes.
func sized(t *testing.T, status, n int) string {
	t.Helper()
	head := []byte(`{"query":"x","results":[]}`)
	body := append(head, bytes.Repeat([]byte(" "), n-len(head))...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// An answer above 16 MiB is ErrTooLarge, whatever its status and whether or
// not a client was injected, and the error does not carry the address.
func TestAPIClientRefusesAnAnswerAbove16MiB(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound} {
		base := sized(t, status, maxAnswer+1)
		for clientName, injected := range map[string]*http.Client{"own client": nil, "injected client": {}} {
			c := newAPIClient(t, base, clientToken, injected)
			_, err := c.SearchArticlesWithResponse(context.Background(), &contract.SearchArticlesParams{Q: "x"})
			if !errors.Is(err, contract.ErrTooLarge) {
				t.Errorf("%d answer of 16 MiB + 1 byte (%s): err %v, want ErrTooLarge", status, clientName, err)
				continue
			}
			if strings.Contains(err.Error(), strings.TrimPrefix(base, "http://")) {
				t.Errorf("ErrTooLarge error %q carries the address", err)
			}
		}
	}
}

// Negative: an answer of exactly 16 MiB is read in full and parsed.
func TestAPIClientReadsAnAnswerOfExactly16MiB(t *testing.T) {
	c := newAPIClient(t, sized(t, http.StatusOK, maxAnswer), clientToken, nil)
	resp, err := c.SearchArticlesWithResponse(context.Background(), &contract.SearchArticlesParams{Q: "x"})
	if err != nil {
		t.Fatalf("an answer of exactly 16 MiB: %v", err)
	}
	if resp.JSON200 == nil || resp.JSON200.Query != "x" {
		t.Errorf("an answer of exactly 16 MiB was not parsed: %+v", resp.JSON200)
	}
}

// closeRecorder is a body that records whether it was closed, and fails the
// read with readErr when that is set.
type closeRecorder struct {
	r       io.Reader
	readErr error
	closed  bool
}

func (b *closeRecorder) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.r.Read(p)
}

func (b *closeRecorder) Close() error { b.closed = true; return nil }

func answer(status int, header http.Header, body string) (*http.Response, *closeRecorder) {
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "application/json")
	b := &closeRecorder{r: strings.NewReader(body)}
	return &http.Response{StatusCode: status, Header: header, Body: b}, b
}

// countingParse is the generated search parser that counts its calls.
func countingParse(n *int) func(*http.Response) (*contract.SearchArticlesResponse, error) {
	return func(r *http.Response) (*contract.SearchArticlesResponse, error) {
		*n++
		return contract.ParseSearchArticlesResponse(r)
	}
}

// Only a 200 reaches the generated parser; it yields the parsed answer and
// neither an APIError nor an error.
func TestParseOKHandsA200ToTheParser(t *testing.T) {
	raw, _ := answer(http.StatusOK, nil, `{"query":"go","results":[]}`)
	var n int
	resp, apiErr, err := contract.ParseOK(raw, countingParse(&n))
	if err != nil || apiErr != nil {
		t.Fatalf("ParseOK(200) = apiErr %+v, err %v, want neither", apiErr, err)
	}
	if n != 1 || resp == nil || resp.JSON200 == nil || resp.JSON200.Query != "go" {
		t.Errorf("ParseOK(200): parser called %d times, answer %+v, want the parsed answer once", n, resp)
	}
}

// Any other status is read by DecodeAPIError alone: the parser is never
// called, so a 429 whose Retry-After is not an integer (which the generated
// parser fails on) is still an APIError, not an error. The body is closed.
func TestParseOKReadsAnyOtherStatusAsAnAPIError(t *testing.T) {
	envelope := `{"error":{"code":"x","message":"boom","details":{"hint":"do this"}}}`
	cases := []struct {
		name   string
		status int
		header http.Header
		body   string
		want   contract.APIError
	}{
		{"not found", http.StatusNotFound, nil, envelope, contract.APIError{Status: 404, Message: "boom", Hint: "do this"}},
		{"429 with words in Retry-After", http.StatusTooManyRequests, http.Header{"Retry-After": {"soon"}}, rateLimited,
			contract.APIError{Status: 429, Message: "too many requests for this API token",
				Hint: "Wait the number of seconds in the Retry-After header, then retry."}},
		{"429 with a wait", http.StatusTooManyRequests, http.Header{"Retry-After": {"30"}}, rateLimited,
			contract.APIError{Status: 429, Message: "too many requests for this API token",
				Hint: "Wait the number of seconds in the Retry-After header, then retry.", RetryAfter: 30}},
		{"html 502", http.StatusBadGateway, nil, "<html>http://10.0.0.1/internal</html>", contract.APIError{Status: 502}},
		{"201 is not a 200", http.StatusCreated, nil, `{"query":"go","results":[]}`, contract.APIError{Status: 201}},
		{"204 is not a 200", http.StatusNoContent, nil, "", contract.APIError{Status: 204}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, body := answer(c.status, c.header, c.body)
			var n int
			resp, apiErr, err := contract.ParseOK(raw, countingParse(&n))
			if err != nil {
				t.Fatalf("ParseOK(%d): err %v, want an APIError", c.status, err)
			}
			if n != 0 {
				t.Errorf("ParseOK(%d) called the parser %d times, want never", c.status, n)
			}
			if resp != nil {
				t.Errorf("ParseOK(%d) returned an answer %+v, want the zero value", c.status, resp)
			}
			if apiErr == nil || *apiErr != c.want {
				t.Errorf("ParseOK(%d) APIError = %+v, want %+v", c.status, apiErr, c.want)
			}
			if !body.closed {
				t.Errorf("ParseOK(%d) left the body open", c.status)
			}
		})
	}
}

// A body that cannot be read, such as one above the cap, is an error, not an
// APIError, and ErrTooLarge stays visible to errors.Is.
func TestParseOKReadFailureIsAnError(t *testing.T) {
	raw, body := answer(http.StatusInternalServerError, nil, "")
	body.readErr = contract.ErrTooLarge
	var n int
	_, apiErr, err := contract.ParseOK(raw, countingParse(&n))
	if !errors.Is(err, contract.ErrTooLarge) || apiErr != nil {
		t.Errorf("ParseOK on an unreadable 500 = apiErr %+v, err %v, want ErrTooLarge and no APIError", apiErr, err)
	}
	if n != 0 {
		t.Errorf("parser called %d times on a 500, want never", n)
	}
}

// A 200 the parser fails on is the parser's error, not an APIError.
func TestParseOKParserFailureIsAnError(t *testing.T) {
	raw, _ := answer(http.StatusOK, nil, `{"query":`)
	_, apiErr, err := contract.ParseOK(raw, contract.ParseSearchArticlesResponse)
	if err == nil || apiErr != nil {
		t.Errorf("ParseOK on a broken 200 = apiErr %+v, err %v, want an error and no APIError", apiErr, err)
	}
}

// #163 (from #223): the client is built only for an http:// or https:// URL
// with a host. Any other address is ErrBaseURL, whose message names nothing
// of the address: it may carry credentials or control characters. NewAPIClient
// is the seam, so the test holds whether the address check stays exported or
// not.
func TestAPIClientRefusesAnAddressThatIsNotAnHTTPURLWithAHost(t *testing.T) {
	for name, addr := range map[string]string{
		"empty":                   "",
		"no scheme":               "goldie.example:8080",
		"no scheme and no port":   "goldie.example",
		"ftp":                     "ftp://goldie.example/",
		"no host":                 "http://",
		"no host but a path":      "https:///api/v1",
		"file":                    "file:///etc/passwd",
		"javascript":              "javascript:alert(1)",
		"a control character":     "http://goldie.example\x1b[2J/",
		"credentials and no host": "http://user:secret@",
	} {
		t.Run(name, func(t *testing.T) {
			c, err := contract.NewAPIClient(addr, clientToken, "test", nil)
			if !errors.Is(err, contract.ErrBaseURL) || c != nil {
				t.Fatalf("NewAPIClient(%q) = %v, %v; want no client and ErrBaseURL", addr, c, err)
			}
			for _, leak := range []string{"secret", "goldie.example", "/etc/passwd", "alert", "\x1b"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("NewAPIClient(%q): error %q leaks %q", addr, err, leak)
				}
			}
		})
	}
}

func TestAPIClientAcceptsAnHTTPOrHTTPSURLWithAHost(t *testing.T) {
	for _, addr := range []string{"http://127.0.0.1:8080", "https://goldie.example", "https://goldie.example/", "http://[::1]:8080/"} {
		if c, err := contract.NewAPIClient(addr, clientToken, "test", nil); err != nil || c == nil {
			t.Errorf("NewAPIClient(%q) = %v, %v; want a client", addr, c, err)
		}
	}
}
