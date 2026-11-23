package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract"
	gmcp "github.com/thschue/goldie/internal/mcp"
)

// mcpPath is where the public listener serves MCP over streamable HTTP.
const mcpPath = "/mcp"

// inProcessBase is the base URL of the contract client behind /mcp. Its
// requests never reach the network, so the host is a fixed placeholder: it is
// never taken from the caller's Host or forwarding headers.
const inProcessBase = "http://goldie.in-process"

// mcpIOTimeout bounds one /mcp request, from reading its body to writing its
// answer, as adminIOTimeout bounds the admin listener: a caller who drips the
// body or never reads the answer cannot keep a request open through the drain
// (PC-08). The tools answer from the API in process, well inside it.
const mcpIOTimeout = 10 * time.Second

// addMCP registers POST /mcp on m: the MCP server of internal/mcp over
// streamable HTTP (ADR 0003, mode all). The route is behind the token like
// every /api/ path, so a request without one is the 401 envelope before MCP
// sees it, and GET and DELETE are the 405 envelope.
//
// The handler is stateless and answers with plain JSON: there are no sessions
// and no standalone event stream, every request is guarded on its own, and
// each is bounded by mcpIOTimeout, so a connected agent never holds up the
// drain (PC-08). Each request gets an MCP
// server whose contract client calls api in process with that request's
// Authorization header, so the caller's token, and only it, reaches the API.
//
// The SDK's localhost Host check is off: it guards unauthenticated local
// servers against DNS rebinding, but here every request needs the token, and
// it would reject a reverse proxy on the same host as the /api/ paths are not.
//
// opts go to each request's MCP server, such as the tool call counter.
//
// A panic in api is recovered into the 500 envelope and logged to logger, the
// server's component logger, as one line naming the API request's method and
// path.
func addMCP(m *publicMux, api http.Handler, logger *slog.Logger, opts ...gmcp.Option) {
	t := handlerTransport{h: api, log: logger}
	h := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		return gmcp.New(transportClient(t, r.Header.Get("Authorization")), opts...)
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	m.handle(http.MethodPost, mcpPath, withDeadline(mcpIOTimeout, h))
}

// withDeadline gives each request to h until d from now: its body reads and
// response writes fail after that, and its context ends. A writer without
// deadlines (none in net/http) still gets the context.
func withDeadline(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline := time.Now().Add(d)
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(deadline)
		_ = rc.SetWriteDeadline(deadline)
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// discardLogger drops every record.
var discardLogger = slog.New(slog.DiscardHandler)

// transportClient is a contract client that hands each request to t and sends
// authorization as its Authorization header.
func transportClient(t handlerTransport, authorization string) contract.ClientInterface {
	c, err := contract.NewClient(inProcessBase,
		contract.WithHTTPClient(&http.Client{Transport: t}),
		contract.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", authorization)
			contract.MarkMCP(r.Header)
			return nil
		}))
	if err != nil {
		// The base URL is a constant that parses; this cannot happen.
		panic("app: in-process contract client: " + err.Error())
	}
	return c
}

// handlerTransport is an http.RoundTripper that serves each request with h:
// no listener, no address, no network. The MCP SDK calls it on a tool
// goroutine that recovers nothing, so a panic in h is recovered here, as
// net/http would for a request, and answered with the 500 envelope instead of
// ending the process. The recovery is logged to log as one line with the
// request's method and escaped path, so the operator the envelope points at
// finds an entry. The panic value is never passed on, nor logged: it may hold
// anything the handler had, a credential included.
type handlerTransport struct {
	h   http.Handler
	log *slog.Logger
}

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body == nil {
		r = r.Clone(r.Context())
		r.Body = http.NoBody
	}
	w := &responseBuffer{header: http.Header{}}
	if serveRecovered(t.h, w, r) {
		// EscapedPath percent-encodes control characters, so the caller's
		// path cannot forge a line even in a sink that is not JSON.
		t.log.ErrorContext(r.Context(), "recovered a panic in an API handler behind /mcp",
			"method", r.Method, "path", r.URL.EscapedPath())
		w = &responseBuffer{header: http.Header{}}
		api.WriteError(w, http.StatusInternalServerError, "internal_error", "the server could not answer",
			"Retry the request; if it keeps failing, ask the operator to check the server logs.")
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", w.status, http.StatusText(w.status)),
		StatusCode:    w.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        w.header,
		Body:          io.NopCloser(&w.body),
		ContentLength: int64(w.body.Len()),
		Request:       r,
	}, nil
}

// serveRecovered serves r with h and reports whether h panicked.
func serveRecovered(h http.Handler, w http.ResponseWriter, r *http.Request) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	h.ServeHTTP(w, r)
	return false
}

// responseBuffer is the http.ResponseWriter of handlerTransport: it keeps the
// status, header and body for the response.
type responseBuffer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *responseBuffer) Header() http.Header { return w.header }

func (w *responseBuffer) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *responseBuffer) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(p)
}
