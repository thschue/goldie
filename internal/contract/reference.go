package contract

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Reference renders the endpoint reference Markdown for an OpenAPI 3.1
// document. The output is deterministic (paths, methods, responses and
// schemas are sorted), so a byte comparison detects drift. Text from the
// spec is escaped for Markdown tables, never interpreted as HTML.
func Reference(spec []byte) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("parse OpenAPI document: %w", err)
	}
	info, _ := doc["info"].(map[string]any)
	paths, _ := doc["paths"].(map[string]any)
	if info == nil || paths == nil {
		return nil, errors.New("OpenAPI document has no info or paths")
	}

	var b strings.Builder
	b.WriteString("<!-- Generated from internal/contract/openapi.yaml by `./run regen`. Do not edit. -->\n\n")
	fmt.Fprintf(&b, "# %s reference\n\n", text(info["title"]))
	fmt.Fprintf(&b, "Version %s. This page documents the public API; the admin endpoints are in the\n", text(info["version"]))
	b.WriteString("[platform contract](../platform-contract.md).\n\n")
	if d := text(info["description"]); d != "" {
		b.WriteString(d + "\n\n")
	}

	b.WriteString("## Endpoints\n\n")
	for _, p := range sortedKeys(paths) {
		ops, _ := paths[p].(map[string]any)
		for _, method := range sortedKeys(ops) {
			op, _ := ops[method].(map[string]any)
			if op == nil {
				continue
			}
			fmt.Fprintf(&b, "### `%s %s`\n\n", strings.ToUpper(method), p)
			if s := text(op["summary"]); s != "" {
				b.WriteString(s + "\n\n")
			}
			if d := text(op["description"]); d != "" {
				b.WriteString(d + "\n\n")
			}
			fmt.Fprintf(&b, "Operation ID: `%s`\n\n", cell(text(op["operationId"])))
			writeResponses(&b, doc, op)
		}
	}

	schemas := lookup(doc, "components", "schemas")
	if len(schemas) > 0 {
		b.WriteString("## Schemas\n\n")
		for _, name := range sortedKeys(schemas) {
			s, _ := schemas[name].(map[string]any)
			fmt.Fprintf(&b, "### `%s`\n\n", cell(name))
			if d := text(s["description"]); d != "" {
				b.WriteString(d + "\n\n")
			}
			writeProperties(&b, s)
		}
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n"), nil
}

func writeResponses(b *strings.Builder, doc, op map[string]any) {
	responses, _ := op["responses"].(map[string]any)
	if len(responses) == 0 {
		return
	}
	b.WriteString("| Status | Description | Body |\n|---|---|---|\n")
	for _, status := range sortedKeys(responses) {
		r, _ := responses[status].(map[string]any)
		if ref := text(r["$ref"]); ref != "" {
			r = resolve(doc, ref)
		}
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", cell(status), cell(text(r["description"])), bodies(r))
	}
	b.WriteString("\n")
}

// bodies lists the response's media types, each with its schema when it has one.
func bodies(r map[string]any) string {
	content, _ := r["content"].(map[string]any)
	if len(content) == 0 {
		return "none"
	}
	var parts []string
	for _, mt := range sortedKeys(content) {
		m, _ := content[mt].(map[string]any)
		schema, _ := m["schema"].(map[string]any)
		parts = append(parts, fmt.Sprintf("`%s` (%s)", cell(mt), typeName(schema)))
	}
	return strings.Join(parts, "<br>")
}

func writeProperties(b *strings.Builder, schema map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return
	}
	required := map[string]bool{}
	if list, ok := schema["required"].([]any); ok {
		for _, r := range list {
			required[text(r)] = true
		}
	}
	b.WriteString("| Property | Type | Required | Description |\n|---|---|---|---|\n")
	for _, name := range sortedKeys(props) {
		p, _ := props[name].(map[string]any)
		req := "no"
		if required[name] {
			req = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", cell(name), typeName(p), req, cell(text(p["description"])))
	}
	b.WriteString("\n")
}

// typeName names a schema: the referenced component, or its type.
func typeName(s map[string]any) string {
	if ref := text(s["$ref"]); ref != "" {
		return "`" + cell(ref[strings.LastIndex(ref, "/")+1:]) + "`"
	}
	if t := text(s["type"]); t != "" {
		return "`" + cell(t) + "`"
	}
	return "any"
}

func resolve(doc map[string]any, ref string) map[string]any {
	path, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil
	}
	return lookup(doc, strings.Split(path, "/")...)
}

func lookup(doc map[string]any, keys ...string) map[string]any {
	cur := doc
	for _, k := range keys {
		next, _ := cur[k].(map[string]any)
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

func text(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// cell makes spec text safe inside a Markdown table cell or code span: one
// line, no pipes, no backticks and no raw HTML.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("|", `\|`, "<", "&lt;", ">", "&gt;", "`", "'").Replace(s)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
