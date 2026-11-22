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

// PublicHandler builds the handler for the public listener (GOLDIE_ADDR). It
// serves the OpenAPI document at /openapi.yaml and /openapi.json; anything
// else is the not-found envelope. Platform stays unaware of the contract:
// this is where the two meet.
func PublicHandler() (http.Handler, error) {
	routes, err := publicRoutes()
	if err != nil {
		return nil, err
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
