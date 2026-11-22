package app

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract/contracttest"
)

// Docs endpoints behind reader auth (#101, from #11): /openapi.yaml,
// /openapi.json and every /swagger-ui path take the same credentials as the
// API, a Bearer token with at least the reader Role, and nothing else.

// specPaths are the OpenAPI documents; docsPaths adds the Swagger UI paths.
var specPaths = []string{"/openapi.yaml", "/openapi.json"}

var docsPaths = append(append([]string{}, specPaths...), swaggerPaths...)

// rawDo is do for the docs paths: the spec does not describe the Swagger UI
// files' 2xx responses, so only spec documents and error responses are
// checked against it.
func rawDo(t *testing.T, method, url string, mutate func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(req)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // test server URL
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode >= 400 || !strings.HasPrefix(resp.Request.URL.Path, "/swagger-ui") {
		contracttest.Conforms(t, resp, body)
	}
	return resp, body
}

// assertUnauthorized checks a docs response is the API's 401: the envelope
// with a bearer hint, a Bearer challenge, and no configured or presented
// secret in the body.
func assertUnauthorized(t *testing.T, what string, resp *http.Response, body []byte, secrets ...string) {
	t.Helper()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("%s: status %d, want 401", what, resp.StatusCode)
		return
	}
	if code, hint := errorHint(t, body); code != "unauthorized" || !strings.Contains(hint, "Bearer") {
		t.Errorf("%s: code %q hint %q, want unauthorized with a bearer hint", what, code, hint)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
		t.Errorf("%s: WWW-Authenticate %q, want Bearer", what, got)
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(string(body), s) {
			t.Errorf("%s: the body echoes a secret: %s", what, body)
		}
	}
}

// The role matrix over the docs endpoints: readers and operators, plaintext
// and hashed, get the documents and the Swagger UI; everyone else gets the
// API's 401.
func TestDocsEndpointsRoleMatrix(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
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
		"reader prefix":    readerToken[:len(readerToken)-1],
		"reader suffix":    readerToken + "x",
		"operator in case": strings.ToUpper(operatorToken),
	}
	allowed := map[string]bool{"reader": true, "operator": true, "hashed reader": true}
	for _, p := range docsPaths {
		for who, token := range callers {
			var auth func(*http.Request)
			if token != "" {
				auth = bearer(token)
			}
			resp, body := rawDo(t, http.MethodGet, srv.URL+p, auth)
			what := p + " as " + who
			if allowed[who] {
				if resp.StatusCode != http.StatusOK {
					t.Errorf("%s: status %d, want 200", what, resp.StatusCode)
				}
				continue
			}
			assertUnauthorized(t, what, resp, body, readerToken, operatorToken, hashedSecret, sha256Hex(hashedSecret))
		}
	}
}

// The GOLDIE_API_TOKEN shorthand opens the docs as it opens the API.
func TestDocsEndpointsTakeTheShorthandToken(t *testing.T) {
	srv := newServer(t)
	for _, p := range docsPaths {
		if resp, _ := rawDo(t, http.MethodGet, srv.URL+p, bearer(testToken)); resp.StatusCode != http.StatusOK {
			t.Errorf("%s with the shorthand token: status %d, want 200", p, resp.StatusCode)
		}
		resp, body := rawDo(t, http.MethodGet, srv.URL+p, nil)
		assertUnauthorized(t, p+" without a token", resp, body, testToken)
	}
}

// Only "Authorization: Bearer <token>" is a credential, as on the API: a
// token in the query string, a cookie, Basic auth or a bare header opens
// nothing, and the 401 never echoes it.
func TestDocsEndpointsRejectCredentialsOutsideTheBearerHeader(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
	basic := func(user string) func(*http.Request) {
		return func(r *http.Request) {
			r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+readerToken)))
		}
	}
	query := func(name string) func(*http.Request) {
		return func(r *http.Request) { r.URL.RawQuery = name + "=" + readerToken }
	}
	cookie := func(name string) func(*http.Request) {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: name, Value: readerToken}) }
	}
	auths := map[string]func(*http.Request){
		"empty bearer":         func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") },
		"scheme only":          func(r *http.Request) { r.Header.Set("Authorization", "Bearer") },
		"token without type":   func(r *http.Request) { r.Header.Set("Authorization", readerToken) },
		"raw basic scheme":     func(r *http.Request) { r.Header.Set("Authorization", "Basic "+readerToken) },
		"basic, token as pass": basic("ci-reader"),
		"basic, empty user":    basic(""),
		"access_token query":   query("access_token"),
		"token query":          query("token"),
		"api_key query":        query("api_key"),
		"authorization query":  query("authorization"),
		"token cookie":         cookie("token"),
		"goldie_token cookie":  cookie("goldie_token"),
		"session cookie":       cookie("__Host-goldie-session"),
		"authorization cookie": cookie("Authorization"),
		"x-api-key header":     func(r *http.Request) { r.Header.Set("X-Api-Key", readerToken) },
		"x-goldie-token":       func(r *http.Request) { r.Header.Set("X-Goldie-Token", readerToken) },
	}
	for _, p := range docsPaths {
		for name, auth := range auths {
			resp, body := rawDo(t, http.MethodGet, srv.URL+p, auth)
			assertUnauthorized(t, p+", "+name, resp, body, readerToken)
		}
	}
}

// HEAD is served wherever GET is, so it is guarded wherever GET is.
func TestDocsEndpointsGuardHEAD(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
	for _, p := range docsPaths {
		resp, _ := rawDo(t, http.MethodHead, srv.URL+p, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("HEAD %s without a token: status %d, want 401", p, resp.StatusCode)
		}
		resp, _ = rawDo(t, http.MethodHead, srv.URL+p, bearer(readerToken))
		if resp.StatusCode != http.StatusOK {
			t.Errorf("HEAD %s as reader: status %d, want 200", p, resp.StatusCode)
		}
	}
}

// Another method on a docs path is checked for the token first, as on the
// API: without one it is the 401, with a reader token the 405 envelope.
func TestDocsEndpointsCheckTheTokenBeforeTheMethod(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
	methods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}
	for _, p := range docsPaths {
		for _, m := range methods {
			resp, body := rawDo(t, m, srv.URL+p, nil)
			assertUnauthorized(t, m+" "+p+" without a token", resp, body)
			resp, body = rawDo(t, m, srv.URL+p, bearer(readerToken))
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s as reader: status %d, want 405", m, p, resp.StatusCode)
				continue
			}
			assertEnvelope(t, body, "method_not_allowed")
		}
	}
}

// Neither a trailing path, a case variant nor a path trick reaches a document
// or the Swagger UI, with or without a token.
func TestDocsPathVariantsServeNoDocument(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
	variants := []string{
		"/openapi.json/", "/openapi.json/x", "/openapi.yaml/", "/openapi.json.bak", "/openapi.json%00",
		"/openapi.json;x", "/openapi", "/openapi.yml", "/OpenAPI.json", "/OPENAPI.JSON", "/OpenAPI.yaml",
		"/Openapi.Yaml", "/Swagger-UI", "/SWAGGER-UI/", "/swagger-UI/swagger-initializer.js",
		"/swagger-ui/Swagger-UI.css", "/swagger-ui/swagger-initializer.js/", "/swagger-ui/x/../swagger-ui.css",
		"/swagger-ui/%2e%2e/openapi.json", "/swagger-ui/..%2fopenapi.json",
		"/api/../openapi.json", "/swagger-ui/../openapi.json",
	}
	for _, v := range variants {
		for who, auth := range map[string]func(*http.Request){"no token": nil, "reader": bearer(readerToken)} {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.URL.Opaque = v // sent exactly as written, not cleaned by the client
			if auth != nil {
				auth(req)
			}
			resp, err := http.DefaultTransport.RoundTrip(req)
			if err != nil {
				t.Fatalf("GET %s: %v", v, err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			what := v + " as " + who
			// A redirect is followed below only if it stays on a docs path
			// that then asks for the token; a 200 here is a document.
			if resp.StatusCode == http.StatusOK {
				t.Errorf("%s: status 200, want no document", what)
			}
			if who == "no token" && resp.StatusCode >= 300 && resp.StatusCode < 400 {
				loc := resp.Header.Get("Location")
				r2, _ := rawDo(t, http.MethodGet, srv.URL+loc, nil)
				if r2.StatusCode == http.StatusOK {
					t.Errorf("%s: redirects to %s, which serves without a token", what, loc)
				}
			}
			for _, marker := range []string{`"openapi"`, "openapi: 3.1", "SwaggerUIBundle", "swagger-ui"} {
				if resp.StatusCode < 300 && strings.Contains(string(body), marker) {
					t.Errorf("%s: body serves %q", what, marker)
				}
			}
		}
	}
}

// A percent-encoded spelling of a docs path is the same path, so it needs the
// token just the same.
func TestDocsPathEncodedSpellingsNeedTheToken(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
	for _, v := range []string{"/%6fpenapi.json", "/openapi%2Eyaml", "/%73wagger-ui", "/swagger-ui/swagger-ui%2Ecss"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.URL.Opaque = v // sent exactly as written
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatalf("GET %s: %v", v, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s without a token: status 200, want no document", v)
		}
		for _, marker := range []string{`"openapi"`, "openapi: 3.1", "SwaggerUIBundle", "swagger-ui"} {
			if resp.StatusCode < 300 && strings.Contains(string(body), marker) {
				t.Errorf("%s without a token: body serves %q", v, marker)
			}
		}
	}
}

// The Swagger UI's 401s and 405s are still under the page's CSP: every
// response from those paths carries it.
func TestSwaggerUIRejectionsKeepTheCSP(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
	for _, p := range swaggerPaths {
		for name, c := range map[string]struct {
			method string
			auth   func(*http.Request)
		}{
			"GET without a token":  {http.MethodGet, nil},
			"GET with a bad token": {http.MethodGet, bearer("wrong-token-0123456789abcdef0123456789")},
			"POST without a token": {http.MethodPost, nil},
			"POST as reader":       {http.MethodPost, bearer(readerToken)},
		} {
			resp, _ := rawDo(t, c.method, srv.URL+p, c.auth)
			if resp.StatusCode < 400 {
				t.Errorf("%s %s: status %d, want a rejection", name, p, resp.StatusCode)
			}
			if csp := resp.Header.Get("Content-Security-Policy"); csp != swaggerUICSP {
				t.Errorf("%s %s: CSP %q, want the Swagger UI policy", name, p, csp)
			}
		}
	}
}

// With auth on, the Swagger UI opened with a reader token loads the spec: the
// page and its assets answer that token, the initializer points at a
// same-origin /openapi.json, and that URL answers the same token with the
// spec. Without a token, or with a wrong one, the page does not open and the
// spec does not load.
func TestSwaggerUILoadsTheSpecWithAReaderToken(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
	asReader := bearer(readerToken)

	resp, page := rawDo(t, http.MethodGet, srv.URL+"/swagger-ui", asReader)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("/swagger-ui as reader: status %d, Content-Type %q, want 200 text/html",
			resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	refs := regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(string(page), -1)
	if len(refs) == 0 {
		t.Fatalf("page references no assets: %s", page)
	}
	for _, m := range refs {
		if r, _ := rawDo(t, http.MethodGet, srv.URL+m[1], asReader); r.StatusCode != http.StatusOK {
			t.Errorf("GET %s as reader: status %d, want 200", m[1], r.StatusCode)
		}
	}

	_, initJS := rawDo(t, http.MethodGet, srv.URL+"/swagger-ui/swagger-initializer.js", asReader)
	m := regexp.MustCompile(`url:\s*"([^"]+)"`).FindStringSubmatch(string(initJS))
	if m == nil || m[1] != "/openapi.json" {
		t.Fatalf("initializer url = %v, want the same-origin /openapi.json", m)
	}
	resp, spec := rawDo(t, http.MethodGet, srv.URL+m[1], asReader)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(spec), `"openapi"`) {
		t.Fatalf("the spec URL with the page's reader token: status %d, want 200 with the document", resp.StatusCode)
	}

	for name, auth := range map[string]func(*http.Request){
		"no token":    nil,
		"wrong token": bearer("wrong-token-0123456789abcdef0123456789"),
	} {
		resp, body := rawDo(t, http.MethodGet, srv.URL+"/swagger-ui", auth)
		assertUnauthorized(t, "/swagger-ui, "+name, resp, body, readerToken)
		resp, body = rawDo(t, http.MethodGet, srv.URL+m[1], auth)
		assertUnauthorized(t, m[1]+" from the page, "+name, resp, body, readerToken)
	}
}

// The page cannot carry the credentials by embedding them: the token that
// opened it appears in no Swagger UI response, the initializer neither drops
// the browser's credentials from its fetches nor stores what "Authorize"
// takes, and nothing reads a token from the URL.
func TestSwaggerUIDoesNotEmbedOrPersistTheToken(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
	for _, p := range swaggerPaths {
		_, body := rawDo(t, http.MethodGet, srv.URL+p, bearer(readerToken))
		if strings.Contains(string(body), readerToken) {
			t.Errorf("%s echoes the token that opened it", p)
		}
	}
	_, initJS := rawDo(t, http.MethodGet, srv.URL+"/swagger-ui/swagger-initializer.js", bearer(readerToken))
	cfg := string(initJS)
	for name, re := range map[string]*regexp.Regexp{
		"drops the browser's credentials": regexp.MustCompile(`credentials\s*[:=]\s*["']omit["']`),
		"stores the token":                regexp.MustCompile(`persistAuthorization\s*:\s*true|localStorage|sessionStorage|document\.cookie`),
		"reads the token from the URL":    regexp.MustCompile(`location\.(search|hash|href)|URLSearchParams`),
	} {
		if re.MatchString(cfg) {
			t.Errorf("initializer %s: %s", name, re.FindString(cfg))
		}
	}
}

// "Authorize" stays how "Try it out" gets its token: the served document, now
// itself behind the token, still declares the global bearerAuth requirement,
// and the documents do not mark themselves anonymous with an empty one.
func TestServedSpecStillDeclaresBearerAuthForTryItOut(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: true})
	resp, body := rawDo(t, http.MethodGet, srv.URL+"/openapi.json", bearer(readerToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/openapi.json as reader: status %d, want 200", resp.StatusCode)
	}
	var doc struct {
		Security   []map[string][]string `json:"security"`
		Components struct {
			SecuritySchemes map[string]struct {
				Type   string `json:"type"`
				Scheme string `json:"scheme"`
			} `json:"securitySchemes"`
		} `json:"components"`
		Paths map[string]map[string]struct {
			Security *[]map[string][]string `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("served JSON does not parse: %v", err)
	}
	scheme, ok := doc.Components.SecuritySchemes["bearerAuth"]
	if !ok || scheme.Type != "http" || !strings.EqualFold(scheme.Scheme, "bearer") {
		t.Errorf("bearerAuth = %+v (declared %v), want an http bearer scheme", scheme, ok)
	}
	global := false
	for _, req := range doc.Security {
		if _, ok := req["bearerAuth"]; ok {
			global = true
		}
	}
	if !global {
		t.Errorf("global security = %v, want bearerAuth", doc.Security)
	}
	for _, p := range specPaths {
		op := doc.Paths[p]["get"]
		if op.Security != nil && len(*op.Security) == 0 {
			t.Errorf("GET %s declares security: [], but it requires the token", p)
		}
	}
}

// Switching the Swagger UI off still leaves its paths at 404 for a reader,
// and the documents stay behind the token.
func TestSwaggerUIDisabledWithAuthOn(t *testing.T) {
	srv := tokenServer(t, Options{APITokens: namedTokenLines(), SwaggerUI: false})
	for _, p := range swaggerPaths {
		resp, body := rawDo(t, http.MethodGet, srv.URL+p, bearer(readerToken))
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s as reader, UI off: status %d, want 404", p, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "not_found")
	}
	for _, p := range specPaths {
		resp, body := rawDo(t, http.MethodGet, srv.URL+p, nil)
		assertUnauthorized(t, p+" without a token, UI off", resp, body)
		if resp, _ := rawDo(t, http.MethodGet, srv.URL+p, bearer(readerToken)); resp.StatusCode != http.StatusOK {
			t.Errorf("%s as reader, UI off: status %d, want 200", p, resp.StatusCode)
		}
	}
}
