package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// maxDecisionsPath bounds the length of config.yaml's decisions.path, which is
// untrusted.
const maxDecisionsPath = 4096

// decisionsFolder is the repository-relative folder, in slash form, that
// config.yaml's `decisions: {path: ...}` names, or "" when it names none. n is
// the decisions node; file is config.yaml for errors. The value is untrusted:
// a non-empty string without control characters, a local relative path. The
// errors name the file and hold no part of the value.
func decisionsFolder(n yaml.Node, file string) (string, error) {
	if !present(n) || (n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
		return "", nil
	}
	if n.Kind != yaml.MappingNode {
		return "", fmt.Errorf("%s: decisions must be a mapping with a path", file)
	}
	var val *yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != "path" {
			return "", fmt.Errorf("%s: decisions has a key other than path", file)
		}
		val = n.Content[i+1]
	}
	if val == nil || (val.Kind == yaml.ScalarNode && val.Tag == "!!null") {
		return "", nil
	}
	rel := val.Value
	if val.Kind != yaml.ScalarNode || val.Tag != "!!str" || rel == "" || len(rel) > maxDecisionsPath ||
		strings.ContainsFunc(rel, unicode.IsControl) || !filepath.IsLocal(rel) {
		return "", errDecisionsPath(file)
	}
	return path.Clean(filepath.ToSlash(rel)), nil
}

func errDecisionsPath(file string) error {
	return fmt.Errorf("%s: decisions.path must name a directory inside the repository, as a relative path", file)
}

// readDecisionsFolder loads the decisions that sit directly in folder, a path
// within fsys (the repository root, so every read stays inside it). Only files
// that look like decisions load: a file without goldie frontmatter that has
// no readable status, or one with a template placeholder, is skipped
// silently, so README.md and a template do not warn. A decision that is
// malformed is not loaded and becomes a warning that names its file; the
// others load. A symlink is never followed. A folder that is missing, a
// symlink or not a directory fails the load. taken holds the lower-cased names
// already loaded in the local Scope; a name in it is a warning, not a second
// decision.
func readDecisionsFolder(fsys fs.FS, folder, file string, taken map[string]bool) (items []Article, warnings []string, err error) {
	if exists, rerr := requireDir(fsys, folder, "the decisions folder"); rerr != nil || !exists {
		return nil, nil, errDecisionsPath(file)
	}
	entries, rerr := fs.ReadDir(fsys, folder)
	if rerr != nil {
		return nil, nil, errDecisionsPath(file)
	}
	for _, e := range entries {
		p := folder + "/" + e.Name()
		if !e.Type().IsRegular() || path.Ext(p) != ".md" {
			continue
		}
		raw, rerr := readLimited(fsys, p, "decision")
		if rerr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, rerr))
			continue
		}
		if !looksLikeDecision(raw) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if taken[strings.ToLower(name)] {
			warnings = append(warnings, p+": a local decision of this name is already loaded")
			continue
		}
		a, perr := parseDecision(name, raw)
		if perr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, perr))
			continue
		}
		taken[strings.ToLower(name)] = true
		a.Scope, a.file = ScopeLocal, p
		items = append(items, a)
	}
	return items, warnings, nil
}

// looksLikeDecision reports whether raw is a decision file to parse. A file
// with goldie frontmatter, or malformed frontmatter, is one. A file without
// is one only when a Nygard or MADR status can be read from it and it is not
// a template placeholder such as {proposed | accepted}.
func looksLikeDecision(raw []byte) bool {
	if _, _, err := splitFrontmatter(raw); !errors.Is(err, errNoFrontmatter) {
		return true
	}
	body := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var meta struct {
		Status string `yaml:"status"`
	}
	if yaml.Unmarshal(bodyDecisionFrontmatter([]byte(body)), &meta) != nil {
		return false
	}
	return meta.Status != "" && !strings.ContainsAny(meta.Status[:1], "{[<")
}
