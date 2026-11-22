package contract

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// specYAML is the OpenAPI 3.1 document, the source of truth for the API.
//
//go:embed openapi.yaml
var specYAML []byte

// SpecYAML returns the OpenAPI document in YAML form. The caller must not
// modify the result.
func SpecYAML() []byte { return specYAML }

// projectID is the ProjectId schema, compiled once from the embedded spec. A
// spec without a usable ProjectId fails at start-up, not per request.
var projectID = mustProjectIDSchema()

type projectIDSchema struct {
	pattern   *regexp.Regexp
	maxLength int
}

func mustProjectIDSchema() projectIDSchema {
	s, err := parseProjectIDSchema(specYAML)
	if err != nil {
		panic(err)
	}
	return s
}

// parseProjectIDSchema reads the pattern and maxLength of the ProjectId schema
// from an OpenAPI document.
func parseProjectIDSchema(spec []byte) (projectIDSchema, error) {
	var doc struct {
		Components struct {
			Schemas struct {
				ProjectID struct {
					Pattern   string `yaml:"pattern"`
					MaxLength int    `yaml:"maxLength"`
				} `yaml:"ProjectId"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return projectIDSchema{}, fmt.Errorf("parse embedded openapi.yaml: %w", err)
	}
	p := doc.Components.Schemas.ProjectID
	if p.Pattern == "" || p.MaxLength <= 0 {
		return projectIDSchema{}, errors.New("openapi.yaml: ProjectId needs a pattern and a maxLength")
	}
	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return projectIDSchema{}, fmt.Errorf("openapi.yaml: ProjectId pattern: %w", err)
	}
	return projectIDSchema{pattern: re, maxLength: p.MaxLength}, nil
}

// ValidProjectID reports whether id is a ProjectId, the one definition of the
// host/org/repo shape (ADR 0018), read from the spec's pattern and maxLength.
// The API checks the project parameter with it and the CLI the Project it
// sends, so neither drifts from the contract. The pattern admits ASCII only,
// so the byte length is the JSON Schema length.
func ValidProjectID(id string) bool {
	return len(id) <= projectID.maxLength && projectID.pattern.MatchString(id)
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
