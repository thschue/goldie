// Package app is the composition root. It wires modules into roles and is the
// only code that may reach the server from the client side (embedded server).
package app

import (
	"fmt"
	"net/http"

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
	handler http.Handler
}

// publicRoutes is the table of public endpoints. PublicHandler registers
// exactly these, and the route-coverage test compares them with the spec, so
// there is no second list to drift. The API operations require the token.
func publicRoutes(snap *engine.Snapshot, token string) ([]route, error) {
	jsonSpec, err := contract.SpecJSON()
	if err != nil {
		return nil, err
	}
	routes := []route{
		{"/openapi.yaml", []string{http.MethodGet}, staticGet("application/yaml", contract.SpecYAML())},
		{"/openapi.json", []string{http.MethodGet}, staticGet("application/json", jsonSpec)},
	}
	for _, r := range api.Routes(snap) {
		routes = append(routes, route{r.Path, []string{r.Method}, api.RequireToken(token, r.Handler)})
	}
	return routes, nil
}

// Options are the settings PublicHandler needs, read from the platform
// configuration by the caller so this package stays the only place that knows
// both modules.
type Options struct {
	SwaggerUI  bool   // serve /swagger-ui (GOLDIE_SWAGGER_UI_ENABLED)
	ContentDir string // the dir Source, loaded once here (GOLDIE_CONTENT_DIR)
	APIToken   string // the bearer token for /api/ (GOLDIE_API_TOKEN)
}

// swaggerUIRoutes is the table of Swagger UI paths. They are not API
// operations, so they are not in the spec and not in publicRoutes, but they are
// registered on the same mux as the API routes, so the auth of #11 can guard
// them too. The static token guards /api/ only, so they are open until then.
func swaggerUIRoutes() ([]route, error) {
	assets, err := contract.SwaggerUIAssets()
	if err != nil {
		return nil, err
	}
	var routes []route
	for _, a := range assets {
		h := withCSP(staticGet(a.ContentType, a.Body))
		if a.Name == "index.html" {
			routes = append(routes,
				route{"/swagger-ui", []string{http.MethodGet}, h},
				route{"/swagger-ui/{$}", []string{http.MethodGet}, h})
			continue
		}
		routes = append(routes, route{"/swagger-ui/" + a.Name, []string{http.MethodGet}, h})
	}
	return routes, nil
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
	routes, err := publicRoutes(snap, opts.APIToken)
	if err != nil {
		return nil, err
	}
	if opts.SwaggerUI {
		ui, err := swaggerUIRoutes()
		if err != nil {
			return nil, err
		}
		routes = append(routes, ui...)
	}
	mux := http.NewServeMux()
	for _, r := range routes {
		mux.Handle(r.path, r.handler)
	}
	notFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint",
			"Check the request path against the OpenAPI document.")
	})
	// Unknown /api/ paths need the token too, so they reveal nothing.
	mux.Handle("/api/", api.RequireToken(opts.APIToken, notFound))
	mux.Handle("/api", api.RequireToken(opts.APIToken, notFound))
	mux.Handle("/", notFound)
	return mux, nil
}

// staticGet serves a fixed body for GET and HEAD and rejects other methods.
func staticGet(contentType string, body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed",
				"Use a method listed in the Allow header.")
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}

// writeError writes the error envelope (ADR 0014); see api.WriteError.
func writeError(w http.ResponseWriter, status int, code, message, hint string) {
	api.WriteError(w, status, code, message, hint)
}
