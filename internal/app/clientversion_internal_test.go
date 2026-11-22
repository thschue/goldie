package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The hint names the minimum without its build metadata, which takes no part
// in precedence and may carry operator-internal names.
func TestUpgradeHintOmitsBuildMetadata(t *testing.T) {
	g, err := newClientGate("1.4.0-rc.1+build.internal.example")
	if err != nil {
		t.Fatal(err)
	}
	h := g.require(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("X-Goldie-Client", "goldie/1.3.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status %d, want 426", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "1.4.0-rc.1 ") || strings.Contains(body, "internal") {
		t.Errorf("hint does not name 1.4.0-rc.1 without its build metadata: %s", body)
	}
}
