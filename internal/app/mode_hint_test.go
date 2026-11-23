package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// In api mode the 404 for /mcp and / says why, not the generic path hint.
func TestAPIModeNotFoundHintNamesTheMode(t *testing.T) {
	srv := newServerWith(t, loadedConfig(t, map[string]string{"GOLDIE_MODE": "api"}))
	for _, p := range []string{"/mcp", "/"} {
		resp, body := uiDo(t, http.MethodGet, srv.URL+p, bearer(testToken))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", p, resp.StatusCode)
		}
		var env struct {
			Error struct {
				Details struct {
					Hint string `json:"hint"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		if h := env.Error.Details.Hint; !strings.Contains(h, "GOLDIE_MODE=api") || !strings.Contains(h, "/mcp") {
			t.Errorf("GET %s: hint %q, want it to name GOLDIE_MODE=api and /mcp", p, h)
		}
	}
}
