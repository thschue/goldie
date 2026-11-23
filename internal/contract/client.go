package contract

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Every goldie client (the CLI and goldie mcp over stdio) reaches the API
// through the client built here, so the clients cannot drift apart on the
// rules that keep a user's token and memory safe from a hostile or broken
// server. Answer reads what a call returns; each client only words what it
// found.

// requestTimeout bounds every API call of a client NewAPIClient builds, unless
// the http.Client it was given sets its own Timeout.
const requestTimeout = 30 * time.Second

// MaxResponseBytes bounds the body of every API answer the client reads.
const MaxResponseBytes = 16 << 20

// ErrRedirect is returned for a 3xx answer, which is never followed: the
// token must never go to a Location the server picks. It carries nothing of
// the answer.
var ErrRedirect = errors.New("the server answered with a redirect")

// ErrTooLarge is returned by an answer body above MaxResponseBytes.
var ErrTooLarge = errors.New("the server's answer is too large")

// ErrBaseURL is returned by CheckBaseURL. Its message names no address.
var ErrBaseURL = errors.New("the server address must be an http:// or https:// URL")

// CheckBaseURL rejects a server address that is not an http:// or https://
// URL with a host.
func CheckBaseURL(baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ErrBaseURL
	}
	return nil
}

// NewAPIClient returns the API client for baseURL. Every request names the
// client as ClientHeaderPrefix+version in ClientHeader and, when token is not
// empty, sends it as a bearer token. No redirect is followed and every answer
// body is capped at MaxResponseBytes, whatever httpc says. A copy of httpc
// (nil means a plain client) is hardened, and httpc is left as it was; the copy
// gets requestTimeout unless httpc sets a Timeout. A baseURL CheckBaseURL
// rejects is ErrBaseURL.
func NewAPIClient(baseURL, token, version string, httpc *http.Client) (*ClientWithResponses, error) {
	return newAPIClient(baseURL, token, ClientHeaderPrefix+version, httpc)
}

// NewMCPAPIClient is NewAPIClient for goldie mcp: the header reads
// goldie/<version> mcp (ClientViaMCP), so the server counts its requests as MCP.
func NewMCPAPIClient(baseURL, token, version string, httpc *http.Client) (*ClientWithResponses, error) {
	return newAPIClient(baseURL, token, ClientHeaderPrefix+version+" "+ClientViaMCP, httpc)
}

func newAPIClient(baseURL, token, clientValue string, httpc *http.Client) (*ClientWithResponses, error) {
	if err := CheckBaseURL(baseURL); err != nil {
		return nil, err
	}
	if httpc == nil {
		httpc = &http.Client{}
	}
	hardened := *httpc
	if hardened.Timeout == 0 {
		hardened.Timeout = requestTimeout
	}
	hardened.Transport = limitTransport{base: httpc.Transport}
	hardened.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	opts := []ClientOption{WithHTTPClient(&hardened),
		WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set(ClientHeader, clientValue)
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			return nil
		})}
	return NewClientWithResponses(strings.TrimRight(baseURL, "/"), opts...)
}

// ParseOK hands raw to the generated parse only when the API answered 200.
// Any other answer is read by DecodeAPIError alone and returned as an
// APIError: the generated parser fails on a 429 whose Retry-After is not an
// integer, which would turn the 429 into a transport failure. A body that
// cannot be read, or a 200 the parser fails on, is an error. raw must come
// from a client NewAPIClient built: ParseOK reads the whole body, and only
// that client's transport caps it at MaxResponseBytes.
func ParseOK[T any](raw *http.Response, parse func(*http.Response) (T, error)) (T, *APIError, error) {
	var zero T
	if raw.StatusCode != http.StatusOK {
		body, err := io.ReadAll(raw.Body)
		_ = raw.Body.Close()
		if err != nil {
			return zero, nil, err
		}
		e := DecodeAPIError(raw.StatusCode, raw.Header, body)
		return zero, &e, nil
	}
	resp, err := parse(raw)
	if err != nil {
		return zero, nil, err
	}
	return resp, nil, nil
}

// limitTransport refuses every 3xx answer and caps every response body at
// MaxResponseBytes, so a hostile or broken server cannot make the client
// buffer an unbounded answer.
type limitTransport struct{ base http.RoundTripper }

func (t limitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := refuseRedirect(t.base, r)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitedBody{rc: resp.Body, left: MaxResponseBytes}
	return resp, nil
}

// refuseRedirect sends r through base (nil means http.DefaultTransport) and
// turns a 3xx answer into ErrRedirect.
func refuseRedirect(base http.RoundTripper, r *http.Request) (*http.Response, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_ = resp.Body.Close()
		return nil, ErrRedirect
	}
	return resp, nil
}

// limitedBody reads at most left bytes and fails with ErrTooLarge when the
// body has more.
type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		var probe [1]byte
		n, err := b.rc.Read(probe[:])
		if n > 0 {
			return 0, ErrTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }
