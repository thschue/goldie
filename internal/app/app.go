// Package app is the composition root. It wires modules into roles and is the
// only code that may reach the server from the client side (embedded server).
package app

import (
	"encoding/json"
	"net/http"

	"github.com/thschue/goldie/internal/contract"
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
// there is no second list to drift.
func publicRoutes() ([]route, error) {
	jsonSpec, err := contract.SpecJSON()
	if err != nil {
		return nil, err
	}
	return []route{
		{"/openapi.yaml", []string{http.MethodGet}, staticGet("application/yaml", contract.SpecYAML())},
		{"/openapi.json", []string{http.MethodGet}, staticGet("application/json", jsonSpec)},
	}, nil
}

// Options are the settings PublicHandler needs, read from the platform
// configuration by the caller so this package stays the only place that knows
// both modules.
type Options struct {
	SwaggerUI bool // serve /swagger-ui (GOLDIE_SWAGGER_UI_ENABLED)
}

// swaggerUIRoutes is the table of Swagger UI paths. They are not API
// operations, so they are not in the spec and not in publicRoutes, but they are
// registered on the same mux as the API routes, so whatever guards that mux
// (the auth of #11) guards them too.
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

// PublicHandler builds the handler for the public listener (GOLDIE_ADDR). It
// serves the OpenAPI document at /openapi.yaml and /openapi.json and, unless
// switched off, Swagger UI at /swagger-ui; anything else is the not-found
// envelope. Platform stays unaware of the contract: this is where the two
// meet.
func PublicHandler(opts Options) (http.Handler, error) {
	routes, err := publicRoutes()
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
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint",
			"Check the request path against the OpenAPI document.")
	})
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

// writeError writes the error envelope {"error":{code,message,details}},
// built from the generated contract types (ADR 0014). hint is the next-step
// hint in details; it must not echo the request or contain addresses, paths
// or stack traces.
func writeError(w http.ResponseWriter, status int, code, message, hint string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(contract.ErrorEnvelope{Error: contract.Error{
		Code:    code,
		Message: message,
		Details: contract.ErrorDetails{Hint: hint},
	}})
}
