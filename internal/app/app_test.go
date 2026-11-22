package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/engine"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newServerWith(t, Options{SwaggerUI: true})
}

// newServerWith starts the public handler; an unset content dir or token is
// the test one.
func newServerWith(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	if opts.ContentDir == "" {
		opts.ContentDir = testContent
	}
	if opts.APIToken == "" {
		opts.APIToken = testToken
	}
	h, err := PublicHandler(opts)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec // test server URL
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	contracttest.Conforms(t, resp, body)
	return resp, body
}

// do sends one request and returns the response with its body read. Every
// response is validated against the spec.
func do(t *testing.T, method, url string, mutate func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader("{}"))
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
	contracttest.Conforms(t, resp, body)
	return resp, body
}

func TestSpecContentTypes(t *testing.T) {
	srv := newServer(t)
	for path, want := range map[string]string{
		"/openapi.yaml": "application/yaml",
		"/openapi.json": "application/json",
	} {
		resp, body := apiGet(t, srv.URL+path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("%s: Content-Type %q, want %q", path, got, want)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options %q, want nosniff", path, got)
		}
		if len(body) == 0 {
			t.Errorf("%s: empty body", path)
		}
	}
}

func TestSpecDocumentsAreEquivalent(t *testing.T) {
	srv := newServer(t)
	_, yamlBody := apiGet(t, srv.URL+"/openapi.yaml")
	_, jsonBody := apiGet(t, srv.URL+"/openapi.json")

	var fromYAML, fromJSON any
	if err := yaml.Unmarshal(yamlBody, &fromYAML); err != nil {
		t.Fatalf("served YAML does not parse: %v", err)
	}
	if err := json.Unmarshal(jsonBody, &fromJSON); err != nil {
		t.Fatalf("served JSON does not parse: %v", err)
	}
	// Round-trip the YAML tree through JSON so number types compare equal.
	b, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatalf("YAML tree is not JSON-representable: %v", err)
	}
	var normalised any
	if err := json.Unmarshal(b, &normalised); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(normalised, fromJSON) {
		t.Error("/openapi.yaml and /openapi.json describe different documents")
	}

	doc, ok := fromJSON.(map[string]any)
	if !ok {
		t.Fatalf("document is %T, want an object", fromJSON)
	}
	if v, _ := doc["openapi"].(string); !strings.HasPrefix(v, "3.1.") {
		t.Errorf("openapi = %q, want 3.1.x", v)
	}
	paths, _ := doc["paths"].(map[string]any)
	for _, p := range []string{"/openapi.yaml", "/openapi.json"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("spec does not describe %s", p)
		}
	}
	schemas, _ := doc["components"].(map[string]any)["schemas"].(map[string]any)
	if _, ok := schemas["ErrorEnvelope"]; !ok {
		t.Error("spec does not define the ErrorEnvelope schema")
	}
}

func TestSpecEndpointsOnlyAnswerGet(t *testing.T) {
	srv := newServer(t)
	resp, body := do(t, http.MethodPost, srv.URL+"/openapi.json", bearer(testToken))
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d, want 405", resp.StatusCode)
	}
	assertEnvelope(t, body, "method_not_allowed")
	if got := resp.Header.Get("Allow"); !strings.Contains(got, "GET") {
		t.Errorf("Allow = %q, want GET", got)
	}
}

func TestUnknownPathIsTheNotFoundEnvelope(t *testing.T) {
	srv := newServer(t)
	resp, body := get(t, srv.URL+"/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
	assertEnvelope(t, body, "not_found")
}

// specDoc is the spec as contracttest loads it.
func specDoc(t *testing.T) map[string]any {
	t.Helper()
	doc, err := contracttest.Spec()
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

var specMethods = []string{"GET", "PUT", "POST", "DELETE", "OPTIONS", "HEAD", "PATCH", "TRACE"}

// specOperations returns "METHOD path" for every operation in the spec.
func specOperations(t *testing.T) []string {
	t.Helper()
	paths, _ := specDoc(t)["paths"].(map[string]any)
	var ops []string
	for path, item := range paths {
		items, _ := item.(map[string]any)
		for key := range items {
			if m := strings.ToUpper(key); slices.Contains(specMethods, m) {
				ops = append(ops, m+" "+path)
			}
		}
	}
	sort.Strings(ops)
	return ops
}

var templateRE = regexp.MustCompile(`\{([^/{}]+)\}`)

// concretePath substitutes every {param} of a spec path template with the
// example of the path parameter of that name, percent-encoded as one path
// segment (ADR 0016). The parameters are those of the path item and of the
// operation; an operation's parameter wins. A parameter without an example is
// an error: the live pass cannot request the path.
func concretePath(doc map[string]any, method, path string) (string, error) {
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[path].(map[string]any)
	op, _ := item[strings.ToLower(method)].(map[string]any)
	examples := map[string]any{}
	for _, list := range []any{item["parameters"], op["parameters"]} {
		params, _ := list.([]any)
		for _, p := range params {
			pm, _ := p.(map[string]any)
			if pm["in"] != "path" {
				continue
			}
			if name, _ := pm["name"].(string); name != "" {
				if ex, ok := pm["example"]; ok {
					examples[name] = ex
				}
			}
		}
	}
	var missing []string
	out := templateRE.ReplaceAllStringFunc(path, func(tok string) string {
		name := tok[1 : len(tok)-1]
		ex, ok := examples[name].(string)
		if !ok || ex == "" {
			missing = append(missing, name)
			return tok
		}
		return url.PathEscape(ex)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s %s: path parameter(s) %v have no string example in the spec", method, path, missing)
	}
	return out, nil
}

func TestConcretePathSubstitutesExamples(t *testing.T) {
	doc := map[string]any{"paths": map[string]any{
		"/a/{id}/b/{n}": map[string]any{
			"parameters": []any{map[string]any{"name": "n", "in": "path", "example": "7"}},
			"get": map[string]any{"parameters": []any{
				map[string]any{"name": "id", "in": "path", "example": "x/y z"},
			}},
		},
		"/c/{id}": map[string]any{
			"get": map[string]any{"parameters": []any{map[string]any{"name": "id", "in": "path"}}},
		},
	}}
	got, err := concretePath(doc, "GET", "/a/{id}/b/{n}")
	if err != nil || got != "/a/x%2Fy%20z/b/7" {
		t.Errorf("concretePath = %q, %v; want /a/x%%2Fy%%20z/b/7", got, err)
	}
	if got, err := concretePath(doc, "GET", "/c/{id}"); err == nil {
		t.Errorf("a parameter without an example gave %q, want an error", got)
	}
}

func TestRoutesAndSpecAgree(t *testing.T) {
	snap, err := engine.NewDirSource(testContent).Load()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := publicRoutes(snap, testToken)
	if err != nil {
		t.Fatal(err)
	}
	var registered []string
	for _, r := range routes {
		for _, m := range r.methods {
			registered = append(registered, m+" "+r.path)
		}
	}
	sort.Strings(registered)
	ops := specOperations(t)

	// Both directions on the table the router is built from.
	for _, r := range registered {
		if !slices.Contains(ops, r) {
			t.Errorf("route %q is registered but not in the spec", r)
		}
	}
	for _, o := range ops {
		if !slices.Contains(registered, o) {
			t.Errorf("operation %q is in the spec but not registered", o)
		}
	}

	// And against the live router: every operation must reach a handler, not
	// the not-found fallback. A templated path is requested with each path
	// parameter's spec example, which the test content answers.
	doc := specDoc(t)
	srv := newServer(t)
	for _, o := range ops {
		method, path, _ := strings.Cut(o, " ")
		if strings.Contains(path, "{") {
			concrete, err := concretePath(doc, method, path)
			if err != nil {
				t.Error(err)
				continue
			}
			path = concrete
		}
		resp, _ := do(t, method, srv.URL+path, bearer(testToken))
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("%s answers %d at %s: the operation is in the spec but the router does not serve it", o, resp.StatusCode, path)
		}
	}
}

// The route table's methods are what the router serves: a method outside them
// is the 405 envelope whose Allow lists exactly the registered methods, with
// HEAD wherever GET is, and HEAD is served where GET is.
func TestRouterHonoursRegisteredMethods(t *testing.T) {
	snap, err := engine.NewDirSource(testContent).Load()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := publicRoutes(snap, testToken)
	if err != nil {
		t.Fatal(err)
	}
	doc := specDoc(t)
	byPath := map[string][]string{}
	for _, r := range routes {
		byPath[r.path] = append(byPath[r.path], r.methods...)
	}
	srv := newServer(t)
	for path, methods := range byPath {
		want := slices.Clone(methods)
		if slices.Contains(methods, http.MethodGet) {
			want = append(want, http.MethodHead)
		}
		sort.Strings(want)
		concrete := path
		if strings.Contains(path, "{") {
			if concrete, err = concretePath(doc, methods[0], path); err != nil {
				t.Error(err)
				continue
			}
		}
		for _, m := range []string{http.MethodDelete, http.MethodPatch} {
			if slices.Contains(methods, m) {
				continue
			}
			resp, body := do(t, m, srv.URL+concrete, bearer(testToken))
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s status %d, want 405", m, path, resp.StatusCode)
				continue
			}
			assertEnvelope(t, body, "method_not_allowed")
			got := strings.Split(strings.ReplaceAll(resp.Header.Get("Allow"), " ", ""), ",")
			sort.Strings(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s %s: Allow = %v, want %v", m, path, got, want)
			}
		}
		if slices.Contains(methods, http.MethodGet) {
			resp, _ := do(t, http.MethodHead, srv.URL+concrete, bearer(testToken))
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
				t.Errorf("HEAD %s answers %d, want it served wherever GET is", path, resp.StatusCode)
			}
		}
	}
}

// leaks matches what an error body must never contain: addresses, ports,
// hostnames, file paths and stack traces.
var leaks = map[string]*regexp.Regexp{
	"IPv4 address": regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`),
	"IPv6 address": regexp.MustCompile(`(?i)\[?[0-9a-f]{0,4}(:[0-9a-f]{0,4}){3,}\]?`),
	"port":         regexp.MustCompile(`:\d{2,5}\b`),
	"hostname":     regexp.MustCompile(`(?i)\b[a-z0-9-]+(\.[a-z0-9-]+)+\b`),
	"file path":    regexp.MustCompile(`(^|[\s"'(=])(\.{0,2}/|[a-zA-Z]:\\)[\w.-]`),
	"stack trace":  regexp.MustCompile(`(?i)goroutine \d|\.go:\d|runtime\.|panic|\bat [\w.]+\(`),
}

func TestErrorEnvelopesDoNotLeak(t *testing.T) {
	srv := newServer(t)
	host := strings.TrimPrefix(srv.URL, "http://")
	type attempt struct {
		method, path string
		mutate       func(*http.Request)
	}
	hostile := func(r *http.Request) { r.Host = "internal.example.test:8443" }
	attempts := []attempt{
		{http.MethodGet, "/nope", nil},
		{http.MethodGet, "/etc/passwd", nil},
		{http.MethodGet, "/..%2f..%2fetc/passwd", nil},
		{http.MethodGet, "/C:/Windows/System32", nil},
		{http.MethodGet, "/10.0.0.1:8080/x.go", nil},
		{http.MethodGet, "/nope", hostile},
		{http.MethodPost, "/openapi.yaml", bearer(testToken)},
		{http.MethodDelete, "/openapi.json", func(r *http.Request) { hostile(r); bearer(testToken)(r) }},
	}
	for _, a := range attempts {
		resp, body := do(t, a.method, srv.URL+a.path, a.mutate)
		if resp.StatusCode < 400 {
			t.Errorf("%s %s: status %d, want an error", a.method, a.path, resp.StatusCode)
			continue
		}
		assertEnvelope(t, body, "")
		for name, re := range leaks {
			if m := re.FindString(string(body)); m != "" {
				t.Errorf("%s %s: error body contains a %s (%q): %s", a.method, a.path, name, m, body)
			}
		}
		for _, echo := range []string{host, "internal.example", a.path, strings.TrimPrefix(a.path, "/")} {
			if echo != "" && strings.Contains(string(body), echo) {
				t.Errorf("%s %s: error body echoes the request (%q): %s", a.method, a.path, echo, body)
			}
		}
	}
}

func TestEveryErrorCarriesAHint(t *testing.T) {
	srv := newServer(t)
	for _, c := range []struct {
		method, path string
		code         string
	}{
		{http.MethodGet, "/nope", "not_found"},
		{http.MethodPost, "/openapi.json", "method_not_allowed"},
	} {
		_, body := do(t, c.method, srv.URL+c.path, bearer(testToken))
		assertEnvelope(t, body, c.code)
	}
}

// assertEnvelope checks body is the error envelope with a next-step hint, and
// the given code when code is not empty.
func assertEnvelope(t *testing.T, body []byte, code string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Hint string `json:"hint"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("body is not the error envelope: %v", err)
	}
	e := env.Error
	if e.Code == "" || (code != "" && e.Code != code) || e.Message == "" || e.Details.Hint == "" {
		t.Errorf("envelope = %+v, want code %q with a message and a details.hint", e, code)
	}
}
