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

// containsKey reports whether key is a mapping key anywhere under n,
// following aliases. A visited set guards against alias cycles.
func containsKey(n *yaml.Node, key string) bool {
	return walkKey(n, key, map[*yaml.Node]bool{})
}

func walkKey(n *yaml.Node, key string, seen map[*yaml.Node]bool) bool {
	if n == nil || seen[n] {
		return false
	}
	seen[n] = true
	switch n.Kind {
	case yaml.AliasNode:
		return walkKey(n.Alias, key, seen)
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key || walkKey(n.Content[i+1], key, seen) {
				return true
			}
		}
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if walkKey(c, key, seen) {
				return true
			}
		}
	}
	return false
}

// parseOperatorPaths reads the operator paths from an OpenAPI document. The
// mark must sit directly on an operation. Anywhere else it would be ignored
// and leave the path open, so it fails startup.
func parseOperatorPaths(spec []byte) (map[string]bool, error) {
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("parse embedded openapi.yaml: %w", err)
	}
	out := map[string]bool{}
	for path, item := range doc.Paths {
		if _, ok := item["$ref"]; ok {
			return nil, fmt.Errorf("openapi.yaml: %s: $ref at path level hides operations from the %s check", path, RoleExtension)
		}
		for method, node := range item {
			if !httpMethods[method] {
				if method == RoleExtension || containsKey(&node, RoleExtension) {
					return nil, fmt.Errorf("openapi.yaml: %s: %s under %q, want it on an operation", path, RoleExtension, method)
				}
				continue // "parameters", "summary" and the like are not operations
			}
			op := &node
			for op.Kind == yaml.AliasNode && op.Alias != nil {
				op = op.Alias
			}
			if op.Kind != yaml.MappingNode {
				continue
			}
			for i := 0; i+1 < len(op.Content); i += 2 {
				k, v := op.Content[i].Value, op.Content[i+1]
				if k != RoleExtension {
					if containsKey(v, RoleExtension) {
						return nil, fmt.Errorf("openapi.yaml: %s %s: %s under %q, want it on the operation", method, path, RoleExtension, k)
					}
					continue
				}
				var role any
				if err := v.Decode(&role); err != nil || role != "operator" {
					return nil, fmt.Errorf("openapi.yaml: %s %s: %s %v, want operator", method, path, RoleExtension, role)
				}
				out[path] = true
			}
		}
	}
	return out, nil
}
