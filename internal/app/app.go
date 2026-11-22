// Package app is the composition root. It wires modules into roles and is the
// only code that may reach the server from the client side (embedded server).
package app

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// route is one public endpoint: a path and the methods it answers. HEAD is
// answered wherever GET is, and is not listed.
type route struct {
	path    string
	methods []string
}

// publicMux is the router of the public listener. api.Server.Register
// registers the spec's operations on it, so the generated code drives routing
// and parameter decoding. It records every route it registers, so the
// route-coverage test reads exactly the table the router is built from; it
// puts every path behind the tokens; and it answers each path's other methods
// with the 405 envelope.
type publicMux struct {
	mux     *http.ServeMux
	tokens  *api.Tokens
	limiter *api.RateLimiter
	routes  []route
}

// newPublicMux returns the router with the spec's operations over snap
// registered, and nothing else, guarded by token as the GOLDIE_API_TOKEN
// shorthand and the default rate limit. A token that fails api.CheckToken
// rejects every request.
func newPublicMux(snap *engine.Snapshot, token string) (*publicMux, error) {
	tokens, err := api.OperatorToken(token)
	if err != nil {
		tokens = &api.Tokens{}
	}
	limiter, err := rateLimiter("")
	if err != nil {
		return nil, err
	}
	return newPublicMuxWith(snap, tokens, limiter)
}

// newPublicMuxWith is newPublicMux guarded by a token set and a rate limiter.
func newPublicMuxWith(snap *engine.Snapshot, tokens *api.Tokens, limiter *api.RateLimiter) (*publicMux, error) {
	srv, err := api.NewServer(snap)
	if err != nil {
		return nil, err
	}
	m := &publicMux{mux: http.NewServeMux(), tokens: tokens, limiter: limiter}
	srv.Register(m)
	return m, nil
}

// publicRoutes is the table of public endpoints as the generated router
// registers them. The route-coverage test compares it with the spec, so there
// is no second list to drift.
func publicRoutes(snap *engine.Snapshot, token string) ([]route, error) {
	m, err := newPublicMux(snap, token)
	if err != nil {
		return nil, err
	}
	return m.routes, nil
}

// HandleFunc registers one generated operation; its pattern is "METHOD path".
// A pattern without a method is a programming error and panics at startup, as
// net/http does for a malformed pattern.
func (m *publicMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || method == "" || path == "" {
		panic(fmt.Sprintf("app: route pattern %q is not \"METHOD path\"", pattern))
	}
	m.handle(method, path, http.HandlerFunc(h))
}

// handle registers h for method on path and, the first time path is seen, the
// 405 envelope for the path's other methods.
func (m *publicMux) handle(method, path string, h http.Handler) {
	if len(m.methods(path)) == 0 {
		m.mux.Handle(path, m.guard(path, m.methodNotAllowed(path)))
	}
	m.routes = append(m.routes, route{path, []string{method}})
	m.mux.Handle(method+" "+path, m.guard(path, h))
}

// docPaths are the OpenAPI documents. Like the Swagger UI they take the token
// but not the rate limit: they are static, and a token that has used up its
// bucket can still read what the API offers.
var docPaths = map[string]bool{"/openapi.yaml": true, "/openapi.json": true}

// guard puts every registered path behind the tokens with at least the reader
// Role: the API under /api/, any operation the spec adds later, the OpenAPI
// documents and the Swagger UI (#101). A new route fails closed. API paths are
// then behind the per-token rate limit, so a request the tokens reject never
// counts against a bucket. Every operation today is open to readers; an
// operator-only one (sync, activity) passes api.RoleOperator when it lands.
// Under /swagger-ui the Content-Security-Policy wraps the token check, so its
// 401s and 405s carry it too.
func (m *publicMux) guard(path string, h http.Handler) http.Handler {
	switch {
	case path == "/swagger-ui" || strings.HasPrefix(path, "/swagger-ui/"):
		return withCSP(m.tokens.Require(api.RoleReader, h))
	case docPaths[path]:
		return m.tokens.Require(api.RoleReader, h)
	}
	return m.tokens.Require(api.RoleReader, m.limiter.Limit(h))
}

// RateLimitRejections returns a copy of the requests rejected by the rate
// limit per token name, for goldie.ratelimit.rejections{token} (#13).
func (m *publicMux) RateLimitRejections() map[string]uint64 { return m.limiter.Rejections() }

// methodNotAllowed answers a method that is not registered for path. The Allow
// header lists the registered ones, with HEAD wherever GET is.
func (m *publicMux) methodNotAllowed(path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var allow []string
		for _, method := range m.methods(path) {
			allow = append(allow, method)
			if method == http.MethodGet {
				allow = append(allow, http.MethodHead)
			}
		}
		w.Header().Set("Allow", strings.Join(allow, ", "))
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed",
			"Use a method listed in the Allow header.")
	})
}

// methods are the methods registered for path, in registration order.
func (m *publicMux) methods(path string) []string {
	var out []string
	for _, r := range m.routes {
		if r.path == path {
			out = append(out, r.methods...)
		}
	}
	return out
}

func (m *publicMux) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

// Options are the settings PublicHandler needs, read from the platform
// configuration by the caller so this package stays the only place that knows
// both modules.
type Options struct {
	SwaggerUI  bool   // serve /swagger-ui (GOLDIE_SWAGGER_UI_ENABLED)
	ContentDir string // the dir Source, loaded once here (GOLDIE_CONTENT_DIR)
	APIToken   string // one operator token, the shorthand (GOLDIE_API_TOKEN)
	APITokens  string // name:role:token lines (GOLDIE_API_TOKENS)
	RateLimit  string // per-token limit, "" for the default (GOLDIE_RATE_LIMIT)
}

// rateLimiter builds the per-token limiter from GOLDIE_RATE_LIMIT. Errors name
// the variable and never contain the value.
func rateLimiter(spec string) (*api.RateLimiter, error) {
	l, err := platform.ParseRateLimit(spec)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", platform.EnvRateLimit, err)
	}
	return api.NewRateLimiter(l.Requests, l.Per, l.Burst), nil
}

// apiTokens builds the token set from exactly one of GOLDIE_API_TOKEN and
// GOLDIE_API_TOKENS. Errors name the variable and never contain a token, a
// digest or a token's length.
func apiTokens(opts Options) (*api.Tokens, error) {
	switch {
	case opts.APIToken != "" && opts.APITokens != "":
		return nil, fmt.Errorf("%s and %s are both set: set exactly one", platform.EnvAPIToken, platform.EnvAPITokens)
	case opts.APIToken == "" && opts.APITokens == "":
		return nil, fmt.Errorf("%s or %s is not set: the API needs a token", platform.EnvAPITokens, platform.EnvAPIToken)
	case opts.APITokens != "":
		tokens, err := api.ParseTokens(opts.APITokens)
		if err != nil {
			return nil, fmt.Errorf("%s %w", platform.EnvAPITokens, err)
		}
		return tokens, nil
	}
	tokens, err := api.OperatorToken(opts.APIToken)
	if err != nil {
		return nil, fmt.Errorf("%s %w", platform.EnvAPIToken, err)
	}
	return tokens, nil
}

// addSwaggerUI registers the Swagger UI paths. They are not API operations, so
// they are not in the spec and not in publicRoutes, but they are registered on
// the same router as the API routes, so the tokens guard them too (#101).
func addSwaggerUI(m *publicMux) error {
	assets, err := contract.SwaggerUIAssets()
	if err != nil {
		return err
	}
	for _, a := range assets {
		h := api.StaticGet(a.ContentType, a.Body)
		if a.Name == "index.html" {
			m.handle(http.MethodGet, "/swagger-ui", h)
			m.handle(http.MethodGet, "/swagger-ui/{$}", h)
			continue
		}
		m.handle(http.MethodGet, "/swagger-ui/"+a.Name, h)
	}
	return nil
}

// swaggerUICSP is the Content-Security-Policy of the Swagger UI paths. The
// page takes bearer tokens, so scripts and requests stay on this origin, eval
// and framing are off, and <base> cannot move relative URLs. Styles allow
// 'unsafe-inline' because Swagger UI sets inline styles; the stylesheet's
// icons are data: images.
const swaggerUICSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// withCSP adds the Swagger UI Content-Security-Policy to every response of h.
func withCSP(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", swaggerUICSP)
		h.ServeHTTP(w, r)
	})
}

// ConfigError is a PublicHandler failure the operator fixes through a setting:
// a missing or malformed token, or content that is missing or does not load.
// Any other PublicHandler error is internal.
type ConfigError struct{ err error }

func (e *ConfigError) Error() string { return e.err.Error() }

func (e *ConfigError) Unwrap() error { return e.err }

// PublicHandler builds the handler for the public listener (GOLDIE_ADDR). It
// loads the dir Source and serves the API under /api/, every path of which
// requires a configured token; the OpenAPI document at /openapi.yaml and
// /openapi.json and, unless switched off, Swagger UI at /swagger-ui, which
// take the same token; and MCP over streamable HTTP at /mcp, which needs a
// token too. Anything else is the
// not-found envelope. A missing token, a bad token line or unloadable content
// is an error naming the variable, so the server fails at startup (PC-04).
// Platform stays unaware of the contract: this is where the two meet.
func PublicHandler(opts Options) (http.Handler, error) {
	tokens, err := apiTokens(opts)
	if err != nil {
		return nil, &ConfigError{err}
	}
	limiter, err := rateLimiter(opts.RateLimit)
	if err != nil {
		return nil, &ConfigError{err}
	}
	if opts.ContentDir == "" {
		return nil, &ConfigError{fmt.Errorf("%s is not set: point it at a content directory", platform.EnvContentDir)}
	}
	snap, err := engine.NewDirSource(opts.ContentDir).Load()
	if err != nil {
		return nil, &ConfigError{fmt.Errorf("%s: %w", platform.EnvContentDir, err)}
	}
	m, err := newPublicMuxWith(snap, tokens, limiter)
	if err != nil {
		return nil, err
	}
	if opts.SwaggerUI {
		if err := addSwaggerUI(m); err != nil {
			return nil, err
		}
	}
	addMCP(m, m)
	notFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint",
			"Check the request path against the OpenAPI document.")
	})
	// Unknown /api/ paths need the token too, so they reveal nothing.
	m.mux.Handle("/api/", m.guard("/api/", notFound))
	m.mux.Handle("/api", m.guard("/api", notFound))
	m.mux.Handle("/", notFound)
	return m, nil
}
