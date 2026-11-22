// Package contracttest checks HTTP responses against the OpenAPI document in
// internal/contract. API tests call Conforms on every response they make, so
// the server cannot drift from the spec unnoticed. It is for tests only.
package contracttest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/thschue/goldie/internal/contract"
)

const specURL = "spec.json"

// Conforms fails the test unless resp, with its already-read body, is a
// response the spec describes for the request that produced it:
//
//   - the operation (method and path) is in the spec and the status code is
//     declared, directly, by range (2XX) or by default;
//   - the Content-Type is one the response declares;
//   - a JSON body validates against the declared schema.
//
// A request the spec has no operation for (an unknown path, or a method the
// path does not allow) must be answered with a 4xx that validates against the
// Error response; that is how the server reports it.
func Conforms(t testing.TB, resp *http.Response, body []byte) {
	t.Helper()
	if err := Check(resp, body); err != nil {
		t.Errorf("%s %s: response does not conform to the OpenAPI document: %v",
			resp.Request.Method, resp.Request.URL.Path, err)
	}
}

// Check is Conforms without the test: it returns the first mismatch.
func Check(resp *http.Response, body []byte) error {
	if resp.Request == nil {
		return errors.New("response has no request")
	}
	doc, c, err := load()
	if err != nil {
		return err
	}
	method := strings.ToLower(resp.Request.Method)
	if method == "head" {
		method = "get"
	}
	// Match the escaped path: a path parameter is one segment, and a "/"
	// inside it arrives as %2F, which the decoded path would split.
	op, opPtr := findOperation(doc, method, resp.Request.URL.EscapedPath())
	var node map[string]any
	var ptr string
	if op == nil {
		if resp.StatusCode < 400 || resp.StatusCode > 499 {
			return fmt.Errorf("no such operation in the spec, and status %d is not a client error", resp.StatusCode)
		}
		node, ptr = resolve(doc, map[string]any{"$ref": "#/components/responses/Error"}, "")
	} else if node, ptr = findResponse(doc, op, opPtr, resp.StatusCode); node == nil {
		return fmt.Errorf("status %d is not declared for the operation", resp.StatusCode)
	}
	if resp.Request.Method == http.MethodHead {
		return nil // a HEAD response has no body to validate
	}
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return fmt.Errorf("Content-Type %q: %w", resp.Header.Get("Content-Type"), err)
	}
	content, _ := node["content"].(map[string]any)
	media, ok := content[mt].(map[string]any)
	if !ok {
		return fmt.Errorf("Content-Type %q is not declared for status %d", mt, resp.StatusCode)
	}
	if _, ok := media["schema"]; !ok || !strings.HasSuffix(mt, "json") {
		return nil // only JSON bodies are validated structurally
	}
	sch, err := c.Compile(specURL + "#" + ptr + "/content/" + escape(mt) + "/schema")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("body is not JSON: %w", err)
	}
	if err := sch.Validate(inst); err != nil {
		return fmt.Errorf("body %s: %w", body, err)
	}
	return nil
}

func load() (map[string]any, *jsonschema.Compiler, error) {
	raw, err := contract.SpecJSON()
	if err != nil {
		return nil, nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(specURL, inst); err != nil {
		return nil, nil, err
	}
	return doc, c, nil
}

// escape encodes one JSON Pointer reference token.
func escape(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}

var paramRE = regexp.MustCompile(`\\\{[^/]*?\\\}`)

// findOperation matches the request path against the spec's path templates.
// When several templates match, the most specific one decides, as OpenAPI
// requires: comparing segment by segment from the left, a literal segment
// beats a templated one. It does not fall back to a less specific template
// that offers the method, so a request the concrete path does not allow is
// reported as unmatched. The result never depends on map order.
func findOperation(doc map[string]any, method, path string) (map[string]any, string) {
	paths, _ := doc["paths"].(map[string]any)
	best := ""
	for tmpl := range paths {
		re := paramRE.ReplaceAllString(regexp.QuoteMeta(tmpl), `[^/]+`)
		if !regexp.MustCompile("^" + re + "$").MatchString(path) {
			continue
		}
		if best == "" || moreSpecific(tmpl, best) {
			best = tmpl
		}
	}
	if best == "" {
		return nil, ""
	}
	if op, ok := paths[best].(map[string]any)[method].(map[string]any); ok {
		return op, "#/paths/" + escape(best) + "/" + method
	}
	return nil, ""
}

// moreSpecific reports whether template a is more specific than b: at the
// first segment where one is literal and the other templated, the literal one
// wins. Equal shapes fall back to string order so the choice stays stable.
func moreSpecific(a, b string) bool {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		at, bt := strings.Contains(as[i], "{"), strings.Contains(bs[i], "{")
		if at != bt {
			return bt
		}
	}
	return a < b
}

// findResponse picks the declared response: exact code, then range, then default.
func findResponse(doc, op map[string]any, opPtr string, status int) (map[string]any, string) {
	responses, _ := op["responses"].(map[string]any)
	for _, key := range []string{strconv.Itoa(status), strconv.Itoa(status/100) + "XX", "default"} {
		if r, ok := responses[key].(map[string]any); ok {
			return resolve(doc, r, opPtr+"/responses/"+key)
		}
	}
	return nil, ""
}

// resolve follows a local $ref and returns the node with its JSON Pointer
// (without the leading "#").
func resolve(doc, node map[string]any, ptr string) (map[string]any, string) {
	ref, ok := node["$ref"].(string)
	if !ok || !strings.HasPrefix(ref, "#/") {
		return node, strings.TrimPrefix(ptr, "#")
	}
	var cur any = doc
	for _, tok := range strings.Split(ref[2:], "/") {
		m, _ := cur.(map[string]any)
		cur = m[strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)]
	}
	next, _ := cur.(map[string]any)
	return next, ref[1:]
}
