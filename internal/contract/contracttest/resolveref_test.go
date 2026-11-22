package contracttest

import (
	"reflect"
	"testing"
)

func TestResolveRef(t *testing.T) {
	target := map[string]any{"description": "found"}
	doc := map[string]any{
		"a/b": map[string]any{"c~d": target},
		"x":   map[string]any{"y": target},
	}
	for name, tc := range map[string]struct {
		node any
		want map[string]any
	}{
		"a local reference resolves":            {map[string]any{"$ref": "#/x/y"}, target},
		"escaped ~1 and ~0 tokens resolve":      {map[string]any{"$ref": "#/a~1b/c~0d"}, target},
		"a dangling reference gives nil":        {map[string]any{"$ref": "#/x/missing"}, nil},
		"a reference through a non-object":      {map[string]any{"$ref": "#/x/y/description/z"}, nil},
		"a reference to a non-object gives nil": {map[string]any{"$ref": "#/x/y/description"}, nil},
		"a nil node gives nil":                  {nil, nil},
		"a string node gives nil":               {"#/x/y", nil},
		"a list node gives nil":                 {[]any{"a"}, nil},
		"a non-local $ref comes back unchanged": {map[string]any{"$ref": "other.json#/x"}, map[string]any{"$ref": "other.json#/x"}},
		"a node without $ref comes back":        {map[string]any{"type": "string"}, map[string]any{"type": "string"}},
	} {
		if got := ResolveRef(doc, tc.node); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
