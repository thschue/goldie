package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	h, err := PublicHandler()
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
	return resp, body
}

func TestSpecContentTypes(t *testing.T) {
	srv := newServer(t)
	for path, want := range map[string]string{
		"/openapi.yaml": "application/yaml",
		"/openapi.json": "application/json",
	} {
		resp, body := get(t, srv.URL+path)
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
	_, yamlBody := get(t, srv.URL+"/openapi.yaml")
	_, jsonBody := get(t, srv.URL+"/openapi.json")

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
	resp, err := http.Post(srv.URL+"/openapi.json", "application/json", strings.NewReader("{}")) //nolint:gosec // test server URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d, want 405", resp.StatusCode)
	}
	assertEnvelope(t, resp, "method_not_allowed")
	if got := resp.Header.Get("Allow"); !strings.Contains(got, "GET") {
		t.Errorf("Allow = %q, want GET", got)
	}
}

func TestUnknownPathIsTheNotFoundEnvelope(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/nope") //nolint:gosec // test server URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
	assertEnvelope(t, resp, "not_found")
}

func assertEnvelope(t *testing.T, resp *http.Response, code string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("body is not the error envelope: %v", err)
	}
	if env.Error.Code != code || env.Error.Message == "" || env.Error.Details == nil {
		t.Errorf("envelope = %+v, want code %q with message and details", env.Error, code)
	}
}
