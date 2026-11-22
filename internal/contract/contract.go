package contract

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// specYAML is the OpenAPI 3.1 document, the source of truth for the API.
//
//go:embed openapi.yaml
var specYAML []byte

// SpecYAML returns the OpenAPI document in YAML form. The caller must not
// modify the result.
func SpecYAML() []byte { return specYAML }

// SpecJSON returns the same document converted to JSON.
func SpecJSON() ([]byte, error) {
	var doc any
	if err := yaml.Unmarshal(specYAML, &doc); err != nil {
		return nil, fmt.Errorf("parse embedded openapi.yaml: %w", err)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("convert openapi.yaml to JSON: %w", err)
	}
	return out, nil
}
