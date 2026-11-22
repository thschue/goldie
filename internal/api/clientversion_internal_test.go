package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// The hint names the minimum without its build metadata, which takes no part
// in precedence and may carry operator-internal names.
func TestUpgradeHintOmitsBuildMetadata(t *testing.T) {
	minimum, err := platform.ParseSemver("1.4.0-rc.1+build.internal.example")
	if err != nil {
		t.Fatal(err)
	}
	h := NewClientVersionGate(minimum).Require(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set(contract.ClientHeader, contract.ClientHeaderPrefix+"1.3.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status %d, want 426", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "1.4.0-rc.1 ") || strings.Contains(body, "internal") {
		t.Errorf("hint does not name 1.4.0-rc.1 without its build metadata: %s", body)
	}
}

// A nil gate, no minimum set, serves every request.
func TestNilClientVersionGateLetsEveryRequestThrough(t *testing.T) {
	var g *ClientVersionGate
	served := false
	h := g.Require(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set(contract.ClientHeader, contract.ClientHeaderPrefix+"0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !served {
		t.Error("a nil gate refused a request")
	}
}
