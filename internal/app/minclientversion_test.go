package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/platform"
)

// Minimum client version (#102). Config.MinClientVersion carries
// GOLDIE_MIN_CLIENT_VERSION as LoadConfig parsed it, once (#186): a typed
// platform.Semver, or the zero Semver for no gate. The CLI sends X-Goldie-Client: goldie/<version> (#150). On
// every path behind the tokens (/api/ and /mcp), a request whose header names
// an older goldie version is refused with 426 in the error envelope, code
// upgrade_required, and a hint that names the minimum and says to upgrade.
//
// The gate sits after the token check and before the rate limit: a request
// without a valid token is still the 401 envelope and learns nothing of the
// gate, and a refused old client spends none of its token's bucket. Requests
// without the header, and requests whose header is not goldie/<semver>
// (curl, other tools, a dev build's goldie/dev), are let through: the header
// is a courtesy for goldie's own CLI, not a credential, so refusing what it
// cannot read would protect nothing and lock out other clients.
//
// Versions compare by semver 2.0.0 precedence: numerically per part, a
// prerelease is older than its release, build metadata is ignored. A leading
// "v" in the header (goldie/v1.4.0) is read as the version after it, because
// release tags carry one (release-please include-v-in-tag).
const (
	envMinClientVersion = "GOLDIE_MIN_CLIENT_VERSION"
	clientHeader        = "X-Goldie-Client"
	minVersion          = "1.4.0"
)

// asClient sends token and X-Goldie-Client: client on a request.
func asClient(token, client string) func(*http.Request) {
	return func(r *http.Request) {
		bearer(token)(r)
		r.Header.Set(clientHeader, client)
	}
}

// gatedServer starts the public handler with the named tokens and minimum.
func gatedServer(t *testing.T, minimum string) *httptest.Server {
	t.Helper()
	return tokenServer(t, platform.Config{APITokens: namedTokenLines(), MinClientVersion: semver(t, minimum)})
}

// serveAs serves one GET in process with the reader token and mutate applied,
// for header values a real client would not send.
func serveAs(t *testing.T, h http.Handler, path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	bearer(readerToken)(req)
	mutate(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// assertUpgradeRequired checks a 426 envelope whose hint names the minimum
// and says to upgrade, and which echoes nothing of the request and leaks
// nothing (ADR 0014). The minimum is the operator's setting, not request
// data, so it is taken out before the leak patterns run: a version looks like
// a hostname to them.
func assertUpgradeRequired(t *testing.T, label string, status int, body []byte, echoes ...string) {
	t.Helper()
	if status != http.StatusUpgradeRequired {
		t.Errorf("%s: status %d, want 426: %s", label, status, body)
		return
	}
	code, hint := errorHint(t, body)
	if code != "upgrade_required" {
		t.Errorf("%s: code %q, want upgrade_required", label, code)
	}
	if !strings.Contains(hint, minVersion) || !strings.Contains(strings.ToLower(hint), "upgrade") {
		t.Errorf("%s: hint %q does not say to upgrade to %s", label, hint, minVersion)
	}
	for _, secret := range []string{readerToken, operatorToken, hashedSecret} {
		if strings.Contains(string(body), secret) {
			t.Errorf("%s: 426 body echoes a secret: %s", label, body)
		}
	}
	for _, echo := range echoes {
		if strings.Contains(string(body), echo) {
			t.Errorf("%s: 426 body echoes the request (%q): %s", label, echo, body)
		}
	}
	rest := strings.ReplaceAll(string(body), minVersion, "")
	for name, re := range leaks {
		if m := re.FindString(rest); m != "" {
			t.Errorf("%s: 426 body contains a %s (%q): %s", label, name, m, body)
		}
	}
}

// An older goldie client gets 426 with the upgrade hint on every /api/ path,
// operations and unknown paths alike, as reader and operator. The response
// conforms to the spec, and the client's own version is not echoed.
func TestOlderClientIsToldToUpgradeInTheEnvelope(t *testing.T) {
	srv := gatedServer(t, minVersion)
	paths := []string{lookupPath, "/api/v1/search?q=kubernetes", "/api/v1/articles/platform%2Fdeploy", "/api/v1/projects", "/api/v1/nope"}
	clients := []string{
		"goldie/1.3.9",
		"goldie/0.1.0",
		"goldie/1.4.0-rc.1",
		"goldie/1.3.99+build.5",
		"goldie/0.9.0-echomarker.7",
	}
	for _, token := range []string{readerToken, operatorToken, hashedSecret} {
		for _, path := range paths {
			for _, client := range clients {
				resp, body := do(t, http.MethodGet, srv.URL+path, asClient(token, client))
				assertUpgradeRequired(t, path+" as "+client, resp.StatusCode, body, "echomarker", client)
			}
		}
	}
}

// A client at the minimum or newer is served, build metadata and a newer
// prerelease included; the comparison is numeric, not by text, so 1.10.0 is
// newer than 1.4.0.
func TestClientAtOrAboveTheMinimumIsServed(t *testing.T) {
	srv := gatedServer(t, minVersion)
	for _, client := range []string{
		"goldie/1.4.0",
		"goldie/1.4.0+build.9",
		"goldie/1.4.1",
		"goldie/1.4.1-rc.1",
		"goldie/1.5.0",
		"goldie/1.10.0",
		"goldie/2.0.0",
		"goldie/10.0.0",
		"goldie/v1.4.0",
	} {
		for _, path := range []string{lookupPath, "/api/v1/search?q=kubernetes"} {
			if resp, body := do(t, http.MethodGet, srv.URL+path, asClient(readerToken, client)); resp.StatusCode != http.StatusOK {
				t.Errorf("%s as %s: status %d, want 200: %s", path, client, resp.StatusCode, body)
			}
		}
	}
}

// Versions compare by semver 2.0.0 precedence (semver.org, section 11).
func TestVersionGateFollowsSemverPrecedence(t *testing.T) {
	for _, c := range []struct {
		minimum, client string
		refused         bool
	}{
		// MAJOR, MINOR and PATCH compare as numbers.
		{"1.2.10", "goldie/1.2.9", true},
		{"1.2.9", "goldie/1.2.10", false},
		{"1.9.0", "goldie/1.10.0", false},
		{"1.10.0", "goldie/1.9.99", true},
		{"9.0.0", "goldie/10.0.0", false},
		{"10.0.0", "goldie/9.99.99", true},
		{"0.2.0", "goldie/0.1.9", true},
		{"0.2.0", "goldie/0.2.0", false},
		{"0.0.0", "goldie/0.0.0", false},
		// A prerelease is older than its release and newer than the one before.
		{"1.4.0", "goldie/1.4.0-rc.1", true},
		{"1.4.0-rc.1", "goldie/1.4.0", false},
		{"1.4.0-rc.1", "goldie/1.3.9", true},
		{"1.4.0-rc.1", "goldie/1.4.0-rc.1", false},
		// Prerelease identifiers: numeric ones as numbers, others in ASCII
		// order, numeric below alphanumeric, more fields above fewer.
		{"1.4.0-rc.2", "goldie/1.4.0-rc.1", true},
		{"1.4.0-rc.2", "goldie/1.4.0-rc.10", false},
		{"1.4.0-beta", "goldie/1.4.0-alpha.9", true},
		{"1.4.0-alpha.beta", "goldie/1.4.0-alpha.1", true},
		{"1.4.0-alpha", "goldie/1.4.0-alpha.1", false},
		{"1.4.0-alpha.1", "goldie/1.4.0-alpha", true},
		// Build metadata is ignored on both sides.
		{"1.4.0", "goldie/1.4.0+any.thing", false},
		{"1.4.0", "goldie/1.3.9+zzz", true},
		{"1.4.0-rc.1", "goldie/1.4.0-rc.1+b.2", false},
		// A leading v in the header is the release tag's.
		{"1.4.0", "goldie/v1.3.0", true},
		{"1.4.0", "goldie/v1.4.0", false},
	} {
		h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), MinClientVersion: semver(t, c.minimum)})
		rec := serveAs(t, h, lookupPath, func(r *http.Request) { r.Header.Set(clientHeader, c.client) })
		want := http.StatusOK
		if c.refused {
			want = http.StatusUpgradeRequired
		}
		if rec.Code != want {
			t.Errorf("minimum %s, %s: status %d, want %d", c.minimum, c.client, rec.Code, want)
		}
	}
}

// Requests that do not name a goldie version are let through, however old
// the minimum: no header (curl, other tools), an empty one, another product,
// a dev build, and every header that is not goldie/<semver>, hostile ones
// included. None of them is answered 426, and none with a server error.
func TestRequestsWithoutAGoldieVersionAreLetThrough(t *testing.T) {
	srv := gatedServer(t, "99.0.0")
	if resp, body := do(t, http.MethodGet, srv.URL+lookupPath, bearer(readerToken)); resp.StatusCode != http.StatusOK {
		t.Errorf("no %s header: status %d, want 200: %s", clientHeader, resp.StatusCode, body)
	}
	for _, client := range []string{
		"curl/8.5.0",
		"Mozilla/5.0 (X11; Linux x86_64)",
		"goldie",
		"goldie/",
		"goldie/dev",
		"goldie/latest",
		"goldie/1",
		"goldie/1.2",
		"goldie/1.2.3.4",
		"goldie/01.2.3",
		"goldie/1.2.3-",
		"goldie/1.2.3-rc_1",
		"goldie/1.2.x",
		"goldie/1.0.0 extra",
		"goldie 1.0.0",
		"goldie/1.0.0/2.0.0",
		"other/1.0.0",
		"goldie/v",
	} {
		if resp, body := do(t, http.MethodGet, srv.URL+lookupPath, asClient(readerToken, client)); resp.StatusCode != http.StatusOK {
			t.Errorf("%s %q: status %d, want 200: %s", clientHeader, client, resp.StatusCode, body)
		}
	}
	h := srv.Config.Handler
	for name, client := range map[string]string{
		"empty":                "",
		"NUL byte":             "goldie/1.0.0\x00",
		"newline":              "goldie/1.0.0\n",
		"tab":                  "goldie/1.0.0\t",
		"zero-width space":     "goldie/1.0.0\u200b",
		"Arabic-Indic digits":  "goldie/\u0661.\u0660.\u0660",
		"fullwidth digits":     "goldie/\uff11.\uff10.\uff10",
		"oversized number":     "goldie/99999999999999999999.0.0",
		"oversized prerelease": "goldie/1.0.0-" + strings.Repeat("a", 64<<10),
		"oversized value":      "goldie/" + strings.Repeat("1", 64<<10) + ".0.0",
		"markup":               "goldie/<script>alert(1)</script>",
	} {
		rec := serveAs(t, h, lookupPath, func(r *http.Request) { r.Header.Set(clientHeader, client) })
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s: status %d, want 200: %s", clientHeader, name, rec.Code, rec.Body)
		}
	}
}

// Unset or empty, there is no gate: the oldest version is served.
func TestNoMinimumMeansNoGate(t *testing.T) {
	for _, opts := range []platform.Config{{APITokens: namedTokenLines()}, {APITokens: namedTokenLines(), MinClientVersion: platform.Semver{}}} {
		srv := tokenServer(t, opts)
		for _, client := range []string{"goldie/0.0.1", "goldie/0.1.0-alpha", "goldie/0.0.0"} {
			if resp, body := do(t, http.MethodGet, srv.URL+lookupPath, asClient(readerToken, client)); resp.StatusCode != http.StatusOK {
				t.Errorf("minimum %s, %s: status %d, want 200: %s", opts.MinClientVersion, client, resp.StatusCode, body)
			}
		}
	}
}

// A request without a valid token is the 401 envelope whatever version it
// names, on /api/ and /mcp: the gate does not turn a 401 into a 426, and an
// unauthenticated caller learns nothing of the gate, neither its status, its
// code, the minimum nor the upgrade text. The public API document does not
// tell it either.
func TestUnauthenticatedRequestsAre401AndLearnNothingOfTheGate(t *testing.T) {
	srv := gatedServer(t, minVersion)
	strangers := map[string]func(*http.Request){
		"no token":      func(r *http.Request) { r.Header.Set(clientHeader, "goldie/0.1.0") },
		"unknown token": asClient("unknown-token-0123456789abcdef0123456789", "goldie/0.1.0"),
		"reader prefix": asClient(readerToken[:len(readerToken)-1], "goldie/0.1.0"),
		"digest":        asClient(sha256Hex(hashedSecret), "goldie/1.3.9"),
		"token name":    asClient("ci-reader", "goldie/1.4.0-rc.1"),
	}
	reveals := func(label string, body []byte) {
		t.Helper()
		lower := strings.ToLower(string(body))
		for _, word := range []string{minVersion, "upgrade", "426", "too old", "minimum"} {
			if strings.Contains(lower, word) {
				t.Errorf("%s: body reveals the gate (%q): %s", label, word, body)
			}
		}
	}
	for who, auth := range strangers {
		for _, path := range []string{lookupPath, "/api/v1/search?q=kubernetes", "/api/v1/nope", "/api"} {
			resp, body := do(t, http.MethodGet, srv.URL+path, auth)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s, %s: status %d, want 401", path, who, resp.StatusCode)
				continue
			}
			if code, _ := errorHint(t, body); code != "unauthorized" {
				t.Errorf("%s, %s: code %q, want unauthorized", path, who, code)
			}
			reveals(path+", "+who, body)
		}
		resp, body := do(t, http.MethodPost, srv.URL+mcpEndpoint, mcpRequest(initializeBody, auth))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("/mcp, %s: status %d, want 401", who, resp.StatusCode)
		}
		reveals("/mcp, "+who, body)
	}
	for _, path := range []string{"/openapi.json", "/openapi.yaml"} {
		resp, body := do(t, http.MethodGet, srv.URL+path, func(r *http.Request) { r.Header.Set(clientHeader, "goldie/0.1.0") })
		if resp.StatusCode == http.StatusUpgradeRequired || strings.Contains(string(body), "upgrade_required") {
			t.Errorf("%s without a token: status %d, the gate answered an unauthenticated request", path, resp.StatusCode)
		}
	}
}

// The gate runs before the rate limit: refused old requests spend none of the
// token's bucket and are not rate-limit rejections, a current client still
// gets its whole burst and then 429, and an old client over the limit is told
// to upgrade, which is what fixes it, rather than to wait.
func TestGateRunsBeforeTheRateLimit(t *testing.T) {
	h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1/m,burst=2"), MinClientVersion: semver(t, minVersion)})
	old := func(r *http.Request) { r.Header.Set(clientHeader, "goldie/1.3.0") }
	current := func(r *http.Request) { r.Header.Set(clientHeader, "goldie/"+minVersion) }
	for i := range 5 {
		if rec := serveAs(t, h, lookupPath, old); rec.Code != http.StatusUpgradeRequired {
			t.Fatalf("old request %d: status %d, want 426", i+1, rec.Code)
		}
	}
	for i := range 2 {
		if rec := serveAs(t, h, lookupPath, current); rec.Code != http.StatusOK {
			t.Fatalf("current request %d after 5 refused old ones: status %d, want 200 (refusals must not spend the bucket)", i+1, rec.Code)
		}
	}
	if rec := serveAs(t, h, lookupPath, current); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("current request over the limit: status %d, want 429", rec.Code)
	}
	rec := serveAs(t, h, lookupPath, old)
	assertUpgradeRequired(t, "old request while the token is limited", rec.Code, rec.Body.Bytes())
	if got := rejections(t, h); len(got) != 1 || got["ci-reader"] != 1 {
		t.Errorf("rate-limit rejections = %v, want only ci-reader: 1 (gate refusals are not rate-limit rejections)", got)
	}
}

// /mcp sits behind the same guard as /api/, so an old goldie client gets the
// 426 envelope there too. An MCP client, which sends no X-Goldie-Client, is
// served, and its tool calls reach the API in process without being refused.
func TestMCPIsGatedLikeTheAPIAndMCPClientsAreServed(t *testing.T) {
	srv := gatedServer(t, minVersion)
	resp, body := do(t, http.MethodPost, srv.URL+mcpEndpoint, mcpRequest(initializeBody, asClient(readerToken, "goldie/1.3.0")))
	assertUpgradeRequired(t, "/mcp as goldie/1.3.0", resp.StatusCode, body)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(readerToken, http.DefaultTransport))
	assertToolsAnswerOverHTTP(t, cs)
}

// setClientVersion sets platform.Version, which the CLI sends in
// X-Goldie-Client, as -ldflags does in a release build, for the length of the
// test.
func setClientVersion(t *testing.T, v string) {
	t.Helper()
	old := platform.Version
	platform.Version = v
	t.Cleanup(func() { platform.Version = old })
}

// The customer's view: an older goldie CLI against a gated server fails with
// the server's upgrade hint, naming the minimum; a CLI at the minimum and a
// dev build are served.
func TestOldCLIIsShownTheUpgradeHint(t *testing.T) {
	srv := newServerWith(t, platform.Config{MinClientVersion: semver(t, minVersion)})
	setClientVersion(t, "1.3.0")
	for _, args := range [][]string{{"search", "kubernetes"}, {"lookup", "agent-loops"}} {
		_, err := runCLI(t, srv.URL, args...)
		if err == nil {
			t.Errorf("%v as goldie/1.3.0: no error, want the upgrade hint", args)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "Hint:") || !strings.Contains(msg, minVersion) || !strings.Contains(strings.ToLower(msg), "upgrade") {
			t.Errorf("%v as goldie/1.3.0: error %q, want the server's hint to upgrade to %s", args, msg, minVersion)
		}
	}
	for _, version := range []string{minVersion, "1.5.0", "dev"} {
		setClientVersion(t, version)
		if out, err := runCLI(t, srv.URL, "lookup", "agent-loops"); err != nil {
			t.Errorf("lookup as goldie/%s: %v, want it served:\n%s", version, err, out)
		}
	}
}

// An invalid GOLDIE_MIN_CLIENT_VERSION fails startup naming the variable,
// without echoing the value. It is parsed once, by LoadConfig (#186), whose
// errors the server reports as invalid configuration; PublicHandler takes
// the parsed Semver and has no second error path.
func TestInvalidMinClientVersionFailsStartupNamingTheVariable(t *testing.T) {
	for _, v := range []string{"marker", "1.2", "dev", "01.2.3", ">=1.2.3", "1.2.3\x00", "goldie/1.2.3", "1.2.3-marker!"} {
		env := map[string]string{platform.EnvContentDir: testContent, platform.EnvAPITokens: namedTokenLines(), envMinClientVersion: v}
		_, err := platform.LoadConfig(func(k string) (string, bool) { s, ok := env[k]; return s, ok })
		if err == nil || !strings.Contains(err.Error(), envMinClientVersion) {
			t.Errorf("%q: err = %v, want a failure naming %s", v, err, envMinClientVersion)
			continue
		}
		if strings.Contains(err.Error(), "marker") {
			t.Errorf("%q: error echoes the value: %v", v, err)
		}
	}
}

// The spec declares the 426 on every /api/ operation, with the error
// envelope as its body.
func TestSpecDeclaresTheUpgradeRequiredResponse(t *testing.T) {
	doc, err := contracttest.Spec()
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := doc["paths"].(map[string]any)
	n := 0
	for path, item := range paths {
		if !strings.HasPrefix(path, "/api/") {
			continue
		}
		ops, _ := item.(map[string]any)
		for method, op := range ops {
			o, ok := op.(map[string]any)
			if !ok || method == "parameters" {
				continue
			}
			if path == "/api/v1/session/providers" {
				// The providers take no bearer token and skip the gate and the limit (#645).
				if sec, ok := o["security"].([]any); !ok || len(sec) != 0 {
					t.Errorf("%s %s: takes no bearer token but is not marked security: []", method, path)
				}
				continue
			}
			if path == "/api/v1/session" && (method == "post" || method == "delete") {
				// Sign-in and sign-out take no bearer token and skip the version gate (ADR 0027).
				if sec, ok := o["security"].([]any); !ok || len(sec) != 0 {
					t.Errorf("%s %s: exempt from the version gate but not marked security: []", method, path)
				}
				if _, has := o["responses"].(map[string]any)["426"]; has {
					t.Errorf("%s %s: declares a 426 it never sends", method, path)
				}
				continue
			}
			n++
			responses, _ := o["responses"].(map[string]any)
			resp := contracttest.ResolveRef(doc, responses["426"])
			if resp == nil {
				t.Errorf("%s %s: no 426 response", method, path)
				continue
			}
			content, _ := resp["content"].(map[string]any)
			media, _ := content["application/json"].(map[string]any)
			schema, _ := media["schema"].(map[string]any)
			if schema["$ref"] != "#/components/schemas/ErrorEnvelope" {
				t.Errorf("%s %s: 426 body schema %v, want the ErrorEnvelope", method, path, schema)
			}
		}
	}
	if n == 0 {
		t.Fatal("the spec has no /api/ operations")
	}
}
