package contract

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

// RoleExtension is the extension that names the lowest token role a rule
// needs. Only "operator" is meaningful. On an operation, the whole operation
// needs the role; on a components/parameters entry, a true value of the
// parameter does (enforced where it is bound, in internal/api). Without it
// the operation or value is open to every token. The extension is additive
// (ADR 0011), so clients and generators ignore it; ADR 0025 records it.
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

// operatorRole is the one role value the mark may carry.
const operatorRole = "operator"

// maxWalkSteps bounds the whole-document walk, so a document of nested aliases
// fails startup instead of exhausting memory.
const maxWalkSteps = 1_000_000

// walkKeys calls visit with the location of every mapping key named key under
// n. An alias is followed at each place it is used, so a mark reached through
// an alias is reported where it is reached, not where it is anchored; stack
// guards against alias cycles.
func walkKeys(n *yaml.Node, key string, path []string, stack map[*yaml.Node]bool, steps *int, visit func(loc string)) error {
	if n == nil || stack[n] {
		return nil
	}
	if *steps++; *steps > maxWalkSteps {
		return fmt.Errorf("openapi.yaml: more than %d nodes, too many aliases to check for %s", maxWalkSteps, key)
	}
	stack[n] = true
	defer delete(stack, n)
	switch n.Kind {
	case yaml.AliasNode:
		return walkKeys(n.Alias, key, path, stack, steps, visit)
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			if k != key && strings.HasPrefix(strings.ToLower(k), "x-goldie-") {
				return fmt.Errorf("openapi.yaml: %s: unknown key %q, want exactly %s", strings.Join(path, " > "), k, key)
			}
			child := append(path[:len(path):len(path)], k)
			if k == key {
				visit(strings.Join(child, " > "))
			}
			if err := walkKeys(n.Content[i+1], key, child, stack, steps, visit); err != nil {
				return err
			}
		}
	case yaml.DocumentNode, yaml.SequenceNode:
		for i, c := range n.Content {
			if err := walkKeys(c, key, append(path[:len(path):len(path)], strconv.Itoa(i)), stack, steps, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// entries calls fn for each key and value of the mapping n (through aliases).
func entries(n *yaml.Node, fn func(key string, val *yaml.Node) error) error {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if err := fn(n.Content[i].Value, n.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}

func child(n *yaml.Node, key string) *yaml.Node {
	var out *yaml.Node
	_ = entries(n, func(k string, v *yaml.Node) error {
		if k == key {
			out = v
		}
		return nil
	})
	return out
}

// parseOperatorPaths reads the operator paths from an OpenAPI document. See
// parseRoles for where the mark may sit.
func parseOperatorPaths(spec []byte) (map[string]bool, error) {
	paths, _, err := parseRoles(spec)
	return paths, err
}

// parseOperatorParameters returns the components/parameters names whose
// object carries the mark. The same whole-document rule as
// parseOperatorPaths applies.
func parseOperatorParameters(spec []byte) (map[string]bool, error) {
	_, params, err := parseRoles(spec)
	return params, err
}

// parseRoles reads the role marks of an OpenAPI document and is fail-closed
// for the whole document: a mark is accepted only directly on an operation
// (it names its path) or directly on a components/parameters entry (it names
// the parameter). A mark anywhere else, including a component an operation
// reaches through $ref or an alias, would be ignored and leave the rule open,
// so it is an error naming the location. An unknown role value is an error.
func parseRoles(spec []byte) (paths, params map[string]bool, err error) {
	var root yaml.Node
	if err := yaml.Unmarshal(spec, &root); err != nil {
		return nil, nil, fmt.Errorf("parse embedded openapi.yaml: %w", err)
	}
	accepted := map[string]bool{} // locations of the marks that sit where they apply
	paths, params = map[string]bool{}, map[string]bool{}
	role := func(loc string, v *yaml.Node) error {
		var r any
		if err := v.Decode(&r); err != nil || r != operatorRole {
			return fmt.Errorf("openapi.yaml: %s: %s %v, want %s", loc, RoleExtension, r, operatorRole)
		}
		return nil
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	err = entries(child(doc, "paths"), func(path string, item *yaml.Node) error {
		if child(item, "$ref") != nil {
			return fmt.Errorf("openapi.yaml: %s: $ref at path level hides operations from the %s check", path, RoleExtension)
		}
		return entries(item, func(method string, op *yaml.Node) error {
			if !httpMethods[method] {
				return nil // "parameters", "summary" and the like are not operations
			}
			if mark := child(op, RoleExtension); mark != nil {
				loc := strings.Join([]string{"paths", path, method, RoleExtension}, " > ")
				if err := role(method+" "+path, mark); err != nil {
					return err
				}
				accepted[loc] = true
				paths[path] = true
			}
			return nil
		})
	})
	if err == nil {
		err = entries(child(child(doc, "components"), "parameters"), func(name string, p *yaml.Node) error {
			if mark := child(p, RoleExtension); mark != nil {
				if err := role("parameter "+name, mark); err != nil {
					return err
				}
				accepted[strings.Join([]string{"components", "parameters", name, RoleExtension}, " > ")] = true
				params[name] = true
			}
			return nil
		})
	}
	if err == nil {
		steps := 0
		var misplaced error
		err = walkKeys(doc, RoleExtension, nil, map[*yaml.Node]bool{}, &steps, func(loc string) {
			if !accepted[loc] && misplaced == nil {
				misplaced = fmt.Errorf("openapi.yaml: %s at %s, want it directly on an operation or on a components/parameters entry", RoleExtension, loc)
			}
		})
		if err == nil {
			err = misplaced
		}
	}
	if err != nil {
		return nil, nil, err
	}
	return paths, params, nil
}
