package contract

import (
	"fmt"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

var (
	anonymousOnce sync.Once
	anonymous     map[string]bool
	anonymousErr  error
)

// AnonymousOperations returns the operations the embedded spec marks with an
// empty `security: []`, as "METHOD /path": they take no bearer token and
// check their own credential (sign-in takes the token in its body). The spec
// states it once and the guard reads it, as with OperatorPaths. The caller
// must not modify the result.
func AnonymousOperations() (map[string]bool, error) {
	anonymousOnce.Do(func() { anonymous, anonymousErr = parseAnonymous(specYAML) })
	return anonymous, anonymousErr
}

// parseAnonymous reads the operations with `security: []`. Any other
// operation-level `security` is an error: only the document-wide bearer
// scheme exists, so anything else would be ignored by the guard.
func parseAnonymous(spec []byte) (map[string]bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(spec, &root); err != nil {
		return nil, fmt.Errorf("parse embedded openapi.yaml: %w", err)
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	out := map[string]bool{}
	err := entries(child(doc, "paths"), func(path string, item *yaml.Node) error {
		return entries(item, func(method string, op *yaml.Node) error {
			sec := deref(child(op, "security"))
			if !httpMethods[method] || sec == nil {
				return nil
			}
			if sec.Kind != yaml.SequenceNode || len(sec.Content) != 0 {
				return fmt.Errorf("openapi.yaml: %s %s: security must be [] (no bearer token) or absent", method, path)
			}
			out[strings.ToUpper(method)+" "+path] = true
			return nil
		})
	})
	return out, err
}
