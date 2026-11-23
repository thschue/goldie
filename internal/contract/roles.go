package contract

import (
	"fmt"
	"sync"

	"go.yaml.in/yaml/v3"
)

// RoleExtension is the operation extension that names the lowest token role
// allowed to call it. Only "operator" is meaningful: an operation without it
// is open to every token. The extension is additive (ADR 0011), so clients
// and generators ignore it.
const RoleExtension = "x-goldie-role"

var (
	operatorPathsOnce sync.Once
	operatorPaths     map[string]bool
	operatorPathsErr  error
)

// OperatorPaths returns the paths with an operation that declares
// `x-goldie-role: operator` in the embedded spec, so the guard and the spec
// state the rule once. The caller must not modify the result. A role other
// than "operator" is an error: a typo must fail startup, not open a path.
func OperatorPaths() (map[string]bool, error) {
	operatorPathsOnce.Do(func() { operatorPaths, operatorPathsErr = parseOperatorPaths(specYAML) })
	return operatorPaths, operatorPathsErr
}

var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// parseOperatorPaths reads the operator paths from an OpenAPI document.
func parseOperatorPaths(spec []byte) (map[string]bool, error) {
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("parse embedded openapi.yaml: %w", err)
	}
	out := map[string]bool{}
	for path, item := range doc.Paths {
		for method, node := range item {
			var op map[string]any
			decodeErr := node.Decode(&op)
			if !httpMethods[method] {
				// The mark belongs on an operation. Anywhere else it would be
				// ignored and leave the path open, so it fails startup.
				if method == RoleExtension || (decodeErr == nil && hasKey(op, RoleExtension)) {
					return nil, fmt.Errorf("openapi.yaml: %s: %s under %q, want it on an operation", path, RoleExtension, method)
				}
				continue // "parameters", "summary" and the like are not operations
			}
			if decodeErr != nil {
				continue
			}
			role, ok := op[RoleExtension]
			if !ok {
				continue
			}
			if role != "operator" {
				return nil, fmt.Errorf("openapi.yaml: %s %s: %s %v, want operator", method, path, RoleExtension, role)
			}
			out[path] = true
		}
	}
	return out, nil
}
