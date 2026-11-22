package contract

import (
	_ "embed"
	"encoding/json"
	"errors"
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

// ProjectIDPattern returns the pattern of the ProjectId schema, the one
// definition of the host/org/repo shape (ADR 0018). The API checks the project
// parameter against it, so server and generated clients agree.
func ProjectIDPattern() (string, error) {
	var doc struct {
		Components struct {
			Schemas struct {
				ProjectID struct {
					Pattern string `yaml:"pattern"`
				} `yaml:"ProjectId"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(specYAML, &doc); err != nil {
		return "", fmt.Errorf("parse embedded openapi.yaml: %w", err)
	}
	p := doc.Components.Schemas.ProjectID.Pattern
	if p == "" {
		return "", errors.New("openapi.yaml: ProjectId has no pattern")
	}
	return p, nil
}

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
