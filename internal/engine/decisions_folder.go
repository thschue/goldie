package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
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

// errDecisionsFolder is a path that is well formed but names no folder here:
// missing, a file or a symlink.
func errDecisionsFolder(file string) error {
	return fmt.Errorf("%s: decisions.path is not a folder in this repository", file)
}

// folderWarning is a warning about one file of the decisions folder: the file
// name is escaped to ASCII like a lookup id (quoteID), and ": " in it is
// escaped so the first ": " splits file from message (contentWarning), then the
// whole is cut like suppressWarning. A file name is untrusted.
func folderWarning(file, msg string) string {
	q := strconv.QuoteToASCII(file)
	w := strings.ReplaceAll(q[1:len(q)-1], ": ", ":\\x20") + ": " + msg
	if len(w) > maxWarningBytes {
		w = w[:maxWarningBytes-3] + "..."
	}
	return w
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
// decision. When .md files remain that read as nothing at all, and none loaded,
// one warning says so.
func readDecisionsFolder(fsys fs.FS, folder, file string, taken map[string]bool) (items []Article, warnings []string, err error) {
	if exists, rerr := requireDir(fsys, folder, "the decisions folder"); rerr != nil || !exists {
		return nil, nil, errDecisionsFolder(file)
	}
	entries, rerr := fs.ReadDir(fsys, folder)
	if rerr != nil {
		return nil, nil, errDecisionsFolder(file)
	}
	unread := 0
	for _, e := range entries {
		p := path.Join(folder, e.Name())
		if !e.Type().IsRegular() || path.Ext(p) != ".md" {
			continue
		}
		raw, rerr := readLimited(fsys, p, "decision")
		if rerr != nil {
			warnings = append(warnings, folderWarning(p, rerr.Error()))
			continue
		}
		switch looksLikeDecision(raw) {
		case notDecision:
			if !strings.EqualFold(e.Name(), "README.md") {
				unread++
			}
			continue
		case templateFile:
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if taken[strings.ToLower(name)] {
			warnings = append(warnings, folderWarning(p, "a local decision of this name is already loaded"))
			continue
		}
		a, perr := parseDecision(name, raw)
		if perr != nil {
			warnings = append(warnings, folderWarning(p, perr.Error()))
			continue
		}
		taken[strings.ToLower(name)] = true
		a.Scope, a.file = ScopeLocal, p
		items = append(items, a)
	}
	if len(items) == 0 && len(warnings) == 0 && unread > 0 {
		warnings = append(warnings, folderWarning(folder, fmt.Sprintf("found %d files but none read as a decision; each needs a status (Nygard `## Status`, MADR `- Status:` or frontmatter)", unread)))
	}
	return items, warnings, nil
}

// How a file of the decisions folder reads.
const (
	isDecision   = iota // goldie frontmatter, or a readable status: parse it
	templateFile        // a status that is a placeholder such as {proposed | accepted}
	notDecision         // no status at all, such as a README
)

// looksLikeDecision tells whether raw is a decision file to parse. A file with
// goldie frontmatter, or malformed frontmatter, is one. A file without is one
// only when a Nygard or MADR status can be read from it.
func looksLikeDecision(raw []byte) int {
	if _, _, err := splitFrontmatter(raw); !errors.Is(err, errNoFrontmatter) {
		return isDecision
	}
	body := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var meta struct {
		Status string `yaml:"status"`
	}
	if yaml.Unmarshal(bodyDecisionFrontmatter([]byte(body)), &meta) != nil || meta.Status == "" {
		return notDecision
	}
	if strings.ContainsAny(meta.Status[:1], "{[<") {
		return templateFile
	}
	return isDecision
}
