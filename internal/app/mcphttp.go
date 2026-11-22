package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/contract"
	gmcp "github.com/thschue/goldie/internal/mcp"
)

// mcpPath is where the public listener serves MCP over streamable HTTP.
const mcpPath = "/mcp"

// inProcessBase is the base URL of the contract client behind /mcp. Its
// requests never reach the network, so the host is a fixed placeholder: it is
// never taken from the caller's Host or forwarding headers.
const inProcessBase = "http://goldie.in-process"

// addMCP registers POST /mcp on m: the MCP server of internal/mcp over
// streamable HTTP (ADR 0003, mode all). The route is behind the token like
// every /api/ path, so a request without one is the 401 envelope before MCP
// sees it, and GET and DELETE are the 405 envelope.
//
// The handler is stateless and answers with plain JSON: there are no sessions
// and no open event streams, so a connected agent never holds up the drain
// (PC-08), and every request is guarded on its own. Each request gets an MCP
// server whose contract client calls api in process with that request's
// Authorization header, so the caller's token, and only it, reaches the API.
//
// The SDK's localhost Host check is off: it guards unauthenticated local
// servers against DNS rebinding, but here every request needs the token, and
// it would reject a reverse proxy on the same host as the /api/ paths are not.
func addMCP(m *publicMux, api http.Handler) {
	h := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		return gmcp.New(inProcessClient(api, r.Header.Get("Authorization")))
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	m.handle(http.MethodPost, mcpPath, h)
}

// inProcessClient is a contract client that hands each request to api and
// sends authorization as its Authorization header.
func inProcessClient(api http.Handler, authorization string) contract.ClientWithResponsesInterface {
	c, err := contract.NewClientWithResponses(inProcessBase,
		contract.WithHTTPClient(&http.Client{Transport: handlerTransport{api}}),
		contract.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", authorization)
			return nil
		}))
	if err != nil {
		// The base URL is a constant that parses; this cannot happen.
		panic("app: in-process contract client: " + err.Error())
	}
	return c
}

// handlerTransport is an http.RoundTripper that serves each request with h:
// no listener, no address, no network.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body == nil {
		r = r.Clone(r.Context())
		r.Body = http.NoBody
	}
	w := &responseBuffer{header: http.Header{}}
	t.h.ServeHTTP(w, r)
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
