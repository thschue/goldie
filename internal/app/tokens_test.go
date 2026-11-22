package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/api"
)

// Named tokens (#99): GOLDIE_API_TOKENS holds name:role:token lines, or
// name:role:sha256:<hex> for a hashed entry. Options.APITokens carries the
// variable's value as LoadConfig read it.
const (
	envAPITokens = "GOLDIE_API_TOKENS"

	readerToken   = "reader-token-0123456789abcdef0123456789"
	operatorToken = "operator-token-0123456789abcdef01234567"
	hashedSecret  = "hashed-secret-0123456789abcdef0123456789"
)

// sha256Hex is the hex digest an operator writes into a hashed entry.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// namedTokenLines configures a plaintext reader, a plaintext operator and a
// hashed reader.
func namedTokenLines() string {
	return "ci-reader:reader:" + readerToken + "\n" +
		"ci-operator:operator:" + operatorToken + "\n" +
		"ci-hashed:reader:sha256:" + sha256Hex(hashedSecret)
}

// tokenServer starts the public handler with only opts' tokens configured:
// newServerWith would add the GOLDIE_API_TOKEN shorthand.
func tokenServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	opts.ContentDir = testContent
	h, err := PublicHandler(opts)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// The role matrix over the operations that exist today: readers and operators
// can search and look up, from plaintext and hashed entries alike, and an
// authenticated miss under /api/ is a 404. Everything else is a 401: no token,
// an unknown one, and the parts of a configured line that are not the secret,
// the digest of a hashed entry above all (it is not a credential). Sync,
// activity, list and bundles get their roles when they land.
func TestNamedTokenRoleMatrix(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines()})
	ops := map[string]int{
		"/api/v1/search?q=kubernetes":  http.StatusOK,
		"/api/v1/articles/agent-loops": http.StatusOK,
		"/api/v1/nope":                 http.StatusNotFound,
	}
	callers := map[string]string{
		"reader":           readerToken,
		"operator":         operatorToken,
		"hashed reader":    hashedSecret,
		"no token":         "",
		"unknown token":    "unknown-token-0123456789abcdef0123456789",
		"digest as token":  sha256Hex(hashedSecret),
		"hashed entry":     "sha256:" + sha256Hex(hashedSecret),
		"token name":       "ci-reader",
		"whole line":       "ci-reader:reader:" + readerToken,
		"role and token":   "reader:" + readerToken,
		"reader prefix":    readerToken[:len(readerToken)-1],
		"reader suffix":    readerToken + "x",
		"secret's prefix":  hashedSecret[:len(hashedSecret)-1],
		"operator in case": strings.ToUpper(operatorToken),
	}
	allowed := map[string]bool{"reader": true, "operator": true, "hashed reader": true}
	for path, okStatus := range ops {
		for who, token := range callers {
			var auth func(*http.Request)
			if token != "" {
				auth = bearer(token)
			}
			resp, body := do(t, http.MethodGet, srv.URL+path, auth)
			want := http.StatusUnauthorized
			if allowed[who] {
				want = okStatus
			}
			if resp.StatusCode != want {
				t.Errorf("%s as %s: status %d, want %d", path, who, resp.StatusCode, want)
				continue
			}
			if want != http.StatusUnauthorized {
				continue
			}
			if code, _ := errorHint(t, body); code != "unauthorized" {
				t.Errorf("%s as %s: code %q, want unauthorized", path, who, code)
			}
			for _, secret := range []string{readerToken, operatorToken, hashedSecret, sha256Hex(hashedSecret)} {
				if strings.Contains(string(body), secret) {
					t.Errorf("%s as %s: the body echoes a configured secret: %s", path, who, body)
				}
			}
		}
	}
}

// GOLDIE_API_TOKEN keeps working as the shorthand for one operator token, and
// it does not make the named tokens' secrets valid or vice versa.
func TestAPITokenShorthandStillAuthenticates(t *testing.T) {
	srv := tokenServer(t, Options{APIToken: testToken})
	for token, want := range map[string]int{
		testToken:     http.StatusOK,
		readerToken:   http.StatusUnauthorized,
		operatorToken: http.StatusUnauthorized,
	} {
		resp, _ := do(t, http.MethodGet, srv.URL+"/api/v1/articles/agent-loops", bearer(token))
		if resp.StatusCode != want {
			t.Errorf("token %.12s…: status %d, want %d", token, resp.StatusCode, want)
		}
	}
}

// Startup fails on a token line that is not name:role:token or
// name:role:sha256:<hex>, on an unknown Role, and on a plaintext token under
// api.MinTokenLength, wherever the line sits. The error names
// GOLDIE_API_TOKENS and is a *ConfigError, but never contains the token, any
// part of it, or its length.
func TestBadTokenLinesFailStartupNamingTheVariable(t *testing.T) {
	short := readerToken[:api.MinTokenLength-1]
	digest := sha256Hex(hashedSecret)
	for name, tc := range map[string]struct {
		lines  string
		hidden []string // must not appear in the error
	}{
		"short plaintext line":        {"ci:reader:" + short, []string{short, short[:8], short[len(short)-8:]}},
		"short plaintext operator":    {"ci:operator:" + short, []string{short}},
		"short line after a good one": {"ok:reader:" + operatorToken + "\nci:reader:" + short, []string{short, operatorToken}},
		"short line before good one":  {"ci:reader:" + short + "\nok:operator:" + operatorToken, []string{short, operatorToken}},
		"empty token":                 {"ci:reader:", nil},
		"no role":                     {"ci:" + readerToken, []string{readerToken}},
		"token only":                  {readerToken, []string{readerToken}},
		"unknown role":                {"ci:admin:" + readerToken, []string{readerToken}},
		"empty role":                  {"ci::" + readerToken, []string{readerToken}},
		"empty name":                  {":reader:" + readerToken, []string{readerToken}},
		"token with a space":          {"ci:reader:" + readerToken[:20] + " " + readerToken[20:], []string{readerToken[:20], readerToken[20:]}},
		"token with a NUL":            {"ci:reader:" + readerToken[:20] + "\x00" + readerToken[20:], []string{readerToken[:20], readerToken[20:]}},
		"hash that is not hex":        {"ci:reader:sha256:" + strings.Repeat("zz", 32), []string{strings.Repeat("zz", 32)}},
		"hash too short":              {"ci:reader:sha256:" + digest[:63], []string{digest[:63]}},
		"hash too long":               {"ci:reader:sha256:" + digest + "0", []string{digest}},
		"empty hash":                  {"ci:reader:sha256:", nil},
	} {
		_, err := PublicHandler(Options{ContentDir: testContent, APITokens: tc.lines})
		if err == nil || !strings.Contains(err.Error(), envAPITokens) {
			t.Errorf("%s: err = %v, want a failure naming %s", name, err, envAPITokens)
			continue
		}
		var cfgErr *ConfigError
		if !errors.As(err, &cfgErr) {
			t.Errorf("%s: err = %v, want a *ConfigError so the caller labels it invalid configuration", name, err)
		}
		for _, h := range tc.hidden {
			if strings.Contains(err.Error(), h) {
				t.Errorf("%s: error echoes a secret: %v", name, err)
			}
		}
		if strings.Contains(err.Error(), "31") {
			t.Errorf("%s: error gives the token's length: %v", name, err)
		}
	}
}

// A plaintext token of exactly api.MinTokenLength bytes is accepted, from
// either Role, next to a hashed entry.
func TestTokenLineAtTheMinimumLengthIsAccepted(t *testing.T) {
	atMin := readerToken[:api.MinTokenLength]
	srv := tokenServer(t, Options{APITokens: "ci:reader:" + atMin + "\nci-hashed:operator:sha256:" + sha256Hex(hashedSecret)})
	for _, token := range []string{atMin, hashedSecret} {
		if resp, _ := do(t, http.MethodGet, srv.URL+"/api/v1/articles/agent-loops", bearer(token)); resp.StatusCode != http.StatusOK {
			t.Errorf("token %.12s…: status %d, want 200", token, resp.StatusCode)
		}
	}
}

// whoami is a probe route behind the token guard that answers with the token
// name api.TokenName finds in the request context.
func whoami(t *testing.T, opts Options) *publicMux {
	t.Helper()
	opts.ContentDir = testContent
	h, err := PublicHandler(opts)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	m, ok := h.(*publicMux)
	if !ok {
		t.Fatalf("PublicHandler returned %T, want the *publicMux the probe route is registered on", h)
	}
	m.HandleFunc("GET /v2/whoami", func(w http.ResponseWriter, r *http.Request) {
		name, ok := api.TokenName(r.Context())
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(name))
	})
	return m
}

func probe(t *testing.T, m *publicMux, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v2/whoami", nil)
	if token != "" {
		bearer(token)(req)
	}
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// The authenticated token's name, never its secret, is in the request context
// for activity (#12) and telemetry (#13): the handler behind the guard sees
// the name of the line that matched, plaintext or hashed.
func TestTokenNameIsInTheRequestContext(t *testing.T) {
	m := whoami(t, Options{APITokens: namedTokenLines()})
	for token, want := range map[string]string{
		readerToken:   "ci-reader",
		operatorToken: "ci-operator",
		hashedSecret:  "ci-hashed",
	} {
		code, got := probe(t, m, token)
		if code != http.StatusOK || got != want {
			t.Errorf("token %.12s…: status %d name %q, want 200 %q", token, code, got, want)
		}
	}
	if code, _ := probe(t, m, ""); code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401 before the handler runs", code)
	}
}

// The GOLDIE_API_TOKEN shorthand has a name too, and it is not the secret.
func TestShorthandTokenHasANameThatIsNotTheSecret(t *testing.T) {
	m := whoami(t, Options{APIToken: testToken})
	code, got := probe(t, m, testToken)
	if code != http.StatusOK || got == "" {
		t.Fatalf("status %d name %q, want 200 with a token name", code, got)
	}
	for _, part := range []string{testToken, testToken[:8], testToken[len(testToken)-8:]} {
		if strings.Contains(got, part) {
			t.Errorf("token name %q contains the secret", got)
		}
	}
}
