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
// puts every path but the OpenAPI documents and Swagger UI behind the token;
// and it answers each path's other methods with the 405 envelope.
type publicMux struct {
	mux    *http.ServeMux
	token  string
	routes []route
	allow  map[string][]string // the methods registered for each path
}

// newPublicMux returns the router with the spec's operations over snap
// registered, and nothing else.
func newPublicMux(snap *engine.Snapshot, token string) (*publicMux, error) {
	srv, err := api.NewServer(snap)
	if err != nil {
		return nil, err
	}
	m := &publicMux{mux: http.NewServeMux(), token: token, allow: map[string][]string{}}
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
func (m *publicMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	method, path, _ := strings.Cut(pattern, " ")
	m.handle(method, path, http.HandlerFunc(h))
}

// handle registers h for method on path and, the first time path is seen, the
// 405 envelope for the path's other methods.
func (m *publicMux) handle(method, path string, h http.Handler) {
	if _, seen := m.allow[path]; !seen {
		m.mux.Handle(path, m.guard(path, m.methodNotAllowed(path)))
	}
	m.allow[path] = append(m.allow[path], method)
	m.routes = append(m.routes, route{path, []string{method}})
	m.mux.Handle(method+" "+path, m.guard(path, h))
}

// openPaths are the registered paths the token does not guard yet: the OpenAPI
// documents. The auth work (#11) covers them and Swagger UI.
var openPaths = map[string]bool{"/openapi.yaml": true, "/openapi.json": true}

// guard adds the Swagger UI Content-Security-Policy when path is under
// /swagger-ui, 405s included, and leaves the OpenAPI documents open. Every
// other path, /api/ and any operation the spec adds later, is behind the
// token: a new route fails closed.
func (m *publicMux) guard(path string, h http.Handler) http.Handler {
	switch {
	case path == "/swagger-ui" || strings.HasPrefix(path, "/swagger-ui/"):
		return withCSP(h)
	case openPaths[path]:
		return h
	}
	return api.RequireToken(m.token, h)
}

// methodNotAllowed answers a method that is not registered for path. The Allow
// header lists the registered ones, with HEAD wherever GET is.
func (m *publicMux) methodNotAllowed(path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var allow []string
		for _, method := range m.allow[path] {
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

func (m *publicMux) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

// Options are the settings PublicHandler needs, read from the platform
// configuration by the caller so this package stays the only place that knows
// both modules.
type Options struct {
	SwaggerUI  bool   // serve /swagger-ui (GOLDIE_SWAGGER_UI_ENABLED)
	ContentDir string // the dir Source, loaded once here (GOLDIE_CONTENT_DIR)
	APIToken   string // the bearer token for /api/ (GOLDIE_API_TOKEN)
}

// addSwaggerUI registers the Swagger UI paths. They are not API operations, so
// they are not in the spec and not in publicRoutes, but they are registered on
// the same router as the API routes, so the auth of #11 can guard them too.
// The static token guards /api/ only, so they are open until then.
func addSwaggerUI(m *publicMux) error {
	assets, err := contract.SwaggerUIAssets()
	if err != nil {
		return err
	}
	for _, a := range assets {
		h := staticGet(a.ContentType, a.Body)
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
// requires the token; the OpenAPI document at /openapi.yaml and
// /openapi.json; and, unless switched off, Swagger UI at /swagger-ui. Anything
// else is the not-found envelope. A missing token or unloadable content is an
// error naming the variable, so the server fails at startup (PC-04). Platform
// stays unaware of the contract: this is where the two meet.
func PublicHandler(opts Options) (http.Handler, error) {
	if err := api.CheckToken(opts.APIToken); err != nil {
		return nil, &ConfigError{fmt.Errorf("%s %w", platform.EnvAPIToken, err)}
	}
	if opts.ContentDir == "" {
		return nil, &ConfigError{fmt.Errorf("%s is not set: point it at a content directory", platform.EnvContentDir)}
	}
	snap, err := engine.NewDirSource(opts.ContentDir).Load()
	if err != nil {
		return nil, &ConfigError{fmt.Errorf("%s: %w", platform.EnvContentDir, err)}
	}
	m, err := newPublicMux(snap, opts.APIToken)
	if err != nil {
		return nil, err
	}
	if opts.SwaggerUI {
		if err := addSwaggerUI(m); err != nil {
			return nil, err
		}
	}
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

// staticGet serves a fixed body. The router sends it GET and HEAD only, and
// HEAD gets no body.
func staticGet(contentType string, body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}
