package contract

import (
	"fmt"
	"sort"
	"sync"

	"go.yaml.in/yaml/v3"
)

var (
	routeTemplatesOnce sync.Once
	routeTemplates     []string
	routeTemplatesErr  error
)

// RouteTemplates returns the path templates of the embedded spec (for example
// /api/v1/articles/{id}), sorted. Callers that must name a route without its
// ids, such as span paths, take them from here. The caller must not modify
// the result.
func RouteTemplates() ([]string, error) {
	routeTemplatesOnce.Do(func() { routeTemplates, routeTemplatesErr = parseRouteTemplates(specYAML) })
	return routeTemplates, routeTemplatesErr
}

func parseRouteTemplates(spec []byte) ([]string, error) {
	var doc struct {
		Paths map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("parse embedded openapi.yaml: %w", err)
	}
	out := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
