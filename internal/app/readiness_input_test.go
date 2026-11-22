package app

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// Issue #239: a request that can never succeed gets the same 400 and hint
// before the first content load as after it, never a 503 not_ready with a
// "retry" hint. The tests serve one provider that has not loaded yet and one
// that has, and compare.

// badInputPaths are requests no Snapshot can make valid.
func badInputPaths() map[string]string {
	return map[string]string{
		"search empty q":         "/api/v1/search?q=",
		"search blank q":         "/api/v1/search?q=%20",
		"search tabs q":          "/api/v1/search?q=%09%20%0A",
		"search over-long q":     "/api/v1/search?q=" + strings.Repeat("a", 1025),
		"search malformed proj":  "/api/v1/search?q=kubernetes&project=acme/shop",
		"search empty proj":      "/api/v1/search?q=kubernetes&project=",
		"search hostile proj":    "/api/v1/search?q=kubernetes&project=github.com/acme/shop%0AInjected",
		"search traversal proj":  "/api/v1/search?q=kubernetes&project=github.com/../etc/passwd",
		"search bad q and proj":  "/api/v1/search?q=%20&project=acme/shop",
		"article malformed proj": "/api/v1/articles/platform%2Fdeploy?project=acme/shop",
		"article empty proj":     "/api/v1/articles/platform%2Fdeploy?project=",
		"article hostile proj":   "/api/v1/articles/platform%2Fdeploy?project=github.com/acme/%3Cscript%3E",
		"article traversal proj": "/api/v1/articles/platform%2Fdeploy?project=github.com/acme/none/../../x",
	}
}

func loadedProvider(t *testing.T) func() *engine.Snapshot {
	t.Helper()
	dir := t.TempDir()
	writeArticle(t, dir, "alpha", "Alpha article", "body")
	snap, err := engine.NewDirSource(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	return staticSnapshot(snap)
}

func TestBadInputIsA400BeforeTheFirstLoadJustAsAfter(t *testing.T) {
	before := providerServer(t, func() *engine.Snapshot { return nil })
	after := providerServer(t, loadedProvider(t))
	for name, path := range badInputPaths() {
		wantResp, wantBody := apiGet(t, after.URL+path)
		if wantResp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: after the load status %d, want 400 (fixture is wrong): %s", name, wantResp.StatusCode, wantBody)
		}
		resp, body := apiGet(t, before.URL+path)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: before the first load status %d, want 400: %s", name, resp.StatusCode, body)
			continue
		}
		code, hint := errorHint(t, body)
		wantCode, wantHint := errorHint(t, wantBody)
		if code != "bad_request" || code != wantCode || hint != wantHint {
			t.Errorf("%s: before the load code %q hint %q, after code %q hint %q; want the same bad_request", name, code, hint, wantCode, wantHint)
		}
		if strings.Contains(strings.ToLower(hint), "retry") {
			t.Errorf("%s: hint %q tells the caller to retry a request that can never succeed", name, hint)
		}
		for _, leak := range []string{"Injected", "script", "passwd", "aaaa"} {
			if strings.Contains(string(body), leak) {
				t.Errorf("%s: body echoes %q: %s", name, leak, body)
			}
		}
	}
}

// The input checks come first, but a valid request before the load is still
// not_ready: an empty result must never read as "no matches".
func TestValidInputBeforeTheFirstLoadIsStillNotReady(t *testing.T) {
	srv := providerServer(t, func() *engine.Snapshot { return nil })
	for _, path := range []string{
		"/api/v1/search?q=kubernetes",
		"/api/v1/search?q=" + strings.Repeat("a", 1024),
		"/api/v1/search?q=kubernetes&project=github.com/acme/shop",
		"/api/v1/articles/platform%2Fdeploy",
		"/api/v1/articles/platform%2Fdeploy?project=github.com/acme/shop",
	} {
		resp, body := apiGet(t, srv.URL+path)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503: %s", path, resp.StatusCode, body)
			continue
		}
		assertEnvelope(t, body, "not_ready")
	}
}

// Authentication still comes before input checks: an anonymous caller learns
// a 401, not what is wrong with the request.
func TestBadInputBeforeTheFirstLoadStillNeedsAToken(t *testing.T) {
	srv := providerServer(t, func() *engine.Snapshot { return nil })
	for name, path := range badInputPaths() {
		resp, body := do(t, http.MethodGet, srv.URL+path, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a token: status %d, want 401", name, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "unauthorized")
	}
}

// The NotReady promise is per server process, like RateLimited: a new replica
// can start without content even when another one has loaded. The spec must
// not claim a load, anywhere, makes not_ready final.
func TestNotReadyDescriptionIsPerServerProcess(t *testing.T) {
	spec, err := contract.SpecJSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	desc := strings.ToLower(strings.Join(strings.Fields(doc.Components.Responses["NotReady"].Description), " "))
	if !strings.Contains(desc, "once this server has loaded its content") {
		t.Errorf("NotReady description %q, want it to say \"once this server has loaded its content\"", desc)
	}
	if strings.Contains(desc, "once a load has succeeded") {
		t.Errorf("NotReady description %q still promises it for any load", desc)
	}
	if !strings.Contains(desc, "never answers this again") {
		t.Errorf("NotReady description %q lost the promise for this server", desc)
	}
}

// The reference docs are regenerated from the spec in the same change.
func TestAPIReferenceCarriesTheNotReadyWording(t *testing.T) {
	b, err := os.ReadFile("../../docs/reference/api.md")
	if err != nil {
		t.Fatal(err)
	}
	ref := strings.ToLower(string(b))
	if !strings.Contains(ref, "once this server has loaded its content") {
		t.Error("docs/reference/api.md lacks the per-process NotReady wording; run ./run regen")
	}
	if strings.Contains(ref, "once a load has succeeded") {
		t.Error("docs/reference/api.md still has the old NotReady wording; run ./run regen")
	}
}
