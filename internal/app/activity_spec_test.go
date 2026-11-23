package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance test for #243, spec first: the served OpenAPI document declares
// GET /api/v1/activity as a text/event-stream behind the bearer token, with
// 401, 403, 429 and 503 in the error envelope, and says the stream is not an
// audit log. The spec lint, the regen drift check and ./run contract-compat
// cover the rest.
func TestServedSpecDeclaresTheActivityStream(t *testing.T) {
	srv := newServerWith(t, platform.Config{})
	resp, body := do(t, http.MethodGet, srv.URL+"/openapi.json", bearer(testToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/openapi.json: status %d", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths[activityPath].(map[string]any)
	op, _ := item["get"].(map[string]any)
	if op == nil {
		t.Fatalf("the served spec has no GET %s", activityPath)
	}
	for method := range item {
		if method != "get" && method != "parameters" && method != "summary" && method != "description" {
			t.Errorf("the spec declares %s %s; the stream is GET only", method, activityPath)
		}
	}
	if _, ok := op["requestBody"]; ok {
		t.Error("GET /api/v1/activity declares a request body")
	}
	// The global bearerAuth applies: no security override opens or narrows it.
	if sec, ok := op["security"]; ok {
		if b, _ := json.Marshal(sec); !strings.Contains(string(b), "bearerAuth") {
			t.Errorf("GET %s overrides security with %s, want the bearer token", activityPath, b)
		}
	}

	responses, _ := op["responses"].(map[string]any)
	ok200 := contracttest.ResolveRef(doc, responses["200"])
	content, _ := ok200["content"].(map[string]any)
	if _, ok := content["text/event-stream"]; !ok {
		t.Errorf("GET %s 200 content %v, want text/event-stream", activityPath, keys(content))
	}
	for _, status := range []string{"401", "403", "429", "503"} {
		r := contracttest.ResolveRef(doc, responses[status])
		if r == nil {
			t.Errorf("GET %s declares no %s response", activityPath, status)
			continue
		}
		c, _ := r["content"].(map[string]any)
		media, _ := c["application/json"].(map[string]any)
		schema, _ := media["schema"].(map[string]any)
		if schema["$ref"] != "#/components/schemas/ErrorEnvelope" {
			t.Errorf("GET %s %s body schema %v, want the ErrorEnvelope", activityPath, status, schema)
		}
	}

	// The docs say the stream is not an audit log: any reader can push older
	// events out of the buffer.
	var text strings.Builder
	for _, v := range []any{op["summary"], op["description"], ok200["description"]} {
		s, _ := v.(string)
		text.WriteString(s + "\n")
	}
	if !strings.Contains(strings.ToLower(text.String()), "not an audit log") {
		t.Errorf("GET %s does not say the stream is not an audit log:\n%s", activityPath, text.String())
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
