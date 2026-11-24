package app

import (
	"fmt"
	"net/http"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract"
	gmcp "github.com/thschue/goldie/internal/mcp"
	"github.com/thschue/goldie/internal/platform"
)

// newPublicMCP builds the public handler of GOLDIE_MODE=mcp: POST /mcp, the
// MCP server of internal/mcp over streamable HTTP as in mode all, with every
// other path answering the 404 envelope. Its tools call the API of
// cfg.UpstreamAddr over the network, and each call carries the Authorization
// header of the request that made it, exactly, and nothing else: the process
// holds no token and the upstream decides what the caller may read (ADR 0003).
// The upstream is fixed at startup, so neither Host nor a forwarding header of
// a request can steer a call, or the caller's token, elsewhere. Errors name
// the variable, never the address.
func newPublicMCP(cfg platform.Config) (*Public, error) {
	if err := checkUpstreamAddr(platform.EnvUpstreamAddr, cfg.UpstreamAddr); err != nil {
		return nil, &ConfigError{err}
	}
	target, err := url.Parse(cfg.UpstreamAddr)
	if err != nil {
		return nil, &ConfigError{fmt.Errorf("%s: not a URL", platform.EnvUpstreamAddr)}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = upstreamHeaderTimeout
	h := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		client, err := contract.NewMCPAPIClient(cfg.UpstreamAddr, "", platform.Version,
			&http.Client{Transport: forwardAuthorization{base: transport, header: r.Header.Get("Authorization")}})
		if err != nil {
			// The address passed checkUpstreamAddr; this cannot happen.
			panic("app: upstream contract client: " + err.Error())
		}
		return gmcp.New(client)
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	post := withDeadline(mcpIOTimeout, h)

	mux := http.NewServeMux()
	mux.HandleFunc(mcpPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed",
				"Use a method listed in the Allow header.")
			return
		}
		post.ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint",
			"This server runs with GOLDIE_MODE=mcp and serves only POST /mcp.")
	})
	return &Public{
		publicMux: &publicMux{mux: mux, endStreams: func() {}},
		source:    source{check: upstreamCheck},
		probe:     upstreamProbe(target),
	}, nil
}

// forwardAuthorization sends every request with header as its Authorization,
// or with none when header is empty, whatever the request held.
type forwardAuthorization struct {
	base   http.RoundTripper
	header string
}

func (f forwardAuthorization) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if f.header == "" {
		r.Header.Del("Authorization")
	} else {
		r.Header.Set("Authorization", f.header)
	}
	return f.base.RoundTrip(r)
}
