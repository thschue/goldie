package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// #635: GOLDIE_MODE selects what the public handler mounts. `all` (the
// default) serves /api, the docs, /mcp and the UI; `api` serves /api and the
// docs, and /mcp and / answer the 404 envelope.
func TestModeSelectsWhatThePublicHandlerMounts(t *testing.T) {
	cases := []struct {
		name string
		mode string // GOLDIE_MODE, unset when empty
		full bool   // /mcp and the UI are mounted
	}{
		{"unset is all", "", true},
		{"all", "all", true},
		{"api", "api", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{platform.EnvSwaggerUIEnabled: "true"}
			if c.mode != "" {
				env["GOLDIE_MODE"] = c.mode
			}
			srv := newServerWith(t, loadedConfig(t, env))

			// /api and the docs are mounted in every mode.
			for _, p := range []string{"/api/v1/search?q=kubernetes", "/openapi.json", "/swagger-ui"} {
				if resp, _ := rawGet(t, srv.URL+p); resp.StatusCode != http.StatusOK {
					t.Errorf("GET %s: status %d, want 200", p, resp.StatusCode)
				}
			}

			// /mcp: mounted in all, the 404 envelope in api.
			resp, body := uiDo(t, http.MethodPost, srv.URL+"/mcp", bearer(testToken))
			switch {
			case c.full && resp.StatusCode == http.StatusNotFound:
				t.Errorf("POST /mcp: status 404, want it mounted")
			case !c.full && resp.StatusCode != http.StatusNotFound:
				t.Errorf("POST /mcp: status %d, want 404", resp.StatusCode)
			case !c.full:
				assertEnvelope(t, body, "not_found")
			}

			// /: the UI page in all, the 404 envelope in api.
			resp, body = uiDo(t, http.MethodGet, srv.URL+"/", bearer(testToken))
			isUI := resp.StatusCode == http.StatusOK && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html")
			switch {
			case c.full && !isUI:
				t.Errorf("GET /: status %d, Content-Type %q, want the UI page", resp.StatusCode, resp.Header.Get("Content-Type"))
			case !c.full && resp.StatusCode != http.StatusNotFound:
				t.Errorf("GET /: status %d, want 404", resp.StatusCode)
			case !c.full:
				assertEnvelope(t, body, "not_found")
			}
		})
	}
}
