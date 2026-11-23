package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// projectFileName is a Project's settings file, directly in its directory:
// projects/<host>/<org>/<repo>/project.yaml.
const projectFileName = "project.yaml"

// maxSuppressions caps the suppress entries of one project.yaml, so a Source
// cannot make a load check without bound.
const maxSuppressions = 1000

// maxWarningBytes caps one sync status warning.
const maxWarningBytes = 1 << 10

// maxYAMLErrorRunes is how much of a YAML decoder message an error keeps; the
// decoder echoes keys, which a Source writes.
const maxYAMLErrorRunes = 200

// projectSettings is the decoded project.yaml. Suppress is a node, not a
// []string, because yaml.v3 decodes a null entry or a number into a string
// without complaint, and both must fail the load.
type projectSettings struct {
	Suppress yaml.Node `yaml:"suppress"`
}

// readProjectFile reads dir/project.yaml and returns its suppress references,
// or none when there is no such file. The file must be a regular file: a
// symlink is rejected even when it stays inside the root, like a symlinked
// articles directory. The error names the file relative to the root.
func readProjectFile(fsys fs.FS, dir string) ([]string, error) {
	p := dir + "/" + projectFileName
	fi, err := fs.Lstat(fsys, p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("%s: %w", p, readErr(err))
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("%s: is a symlink; project.yaml must be a regular file", p)
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%s: is not a regular file; project.yaml must be one", p)
	}
	raw, err := readLimited(fsys, p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	refs, err := parseProjectFile(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return refs, nil
}

// parseProjectFile decodes one YAML document strictly: unknown keys,
// duplicate keys and a second document fail. A file with no document, only
// comments, suppresses nothing. suppress must be a list of strings, each a
// well-formed reference (ADR 0021); aliases and other node kinds fail.
func parseProjectFile(raw []byte) ([]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var s projectSettings
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, yamlErr(err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("holds more than one YAML document")
	}
	if !present(s.Suppress) {
		return nil, nil
	}
	if s.Suppress.Kind != yaml.SequenceNode {
		return nil, errors.New("suppress must be a list of references of the form <kind>:<name>")
	}
	if len(s.Suppress.Content) > maxSuppressions {
		return nil, fmt.Errorf("suppress has more than %d entries", maxSuppressions)
	}
	refs := make([]string, 0, len(s.Suppress.Content))
	for i, n := range s.Suppress.Content {
		v, ok := scalar(*n, "!!str")
		if !ok {
			return nil, fmt.Errorf("suppress entry %d is not a reference of the form <kind>:<name>", i+1)
		}
		if _, err := parseRef(v); err != nil {
			return nil, fmt.Errorf("suppress %s: %w", quoteRef(v), err)
		}
		refs = append(refs, v)
	}
	return refs, nil
}

// yamlTypeName is the Go type yaml.v3 names in a type error; it is noise to a
// content author.
var yamlTypeName = regexp.MustCompile(` (in type|into) [\w.*\[\]{}]+`)

// yamlErr renders a decoder error for a load error. yaml.v3 echoes keys and
// values from the file, so the message is cut to maxYAMLErrorRunes runes and
// every rune outside printable ASCII is escaped.
func yamlErr(err error) error {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	msg = strings.ReplaceAll(msg, "unmarshal errors:\n  ", "")
	msg = yamlTypeName.ReplaceAllString(msg, "")
	if utf8.RuneCountInString(msg) > maxYAMLErrorRunes {
		msg = string([]rune(msg)[:maxYAMLErrorRunes]) + "…"
	}
	q := strconv.QuoteToASCII(msg)
	return fmt.Errorf("malformed project.yaml: %s", q[1:len(q)-1])
}

// applySuppressions resolves a Project's suppress references in its own
// Resolution view, published Items only (#353): its published copy first, then
// the published global Item. A reference to the Project's own Item, or to an
// enforced global Item, fails the load. One that resolves to nothing, a draft
// included, is a warning, so another team's deletion never fails this Project.
// It is called only while loading, after the Project is added.
func (s *Snapshot) applySuppressions(scope string, refs []string) error {
	if len(refs) == 0 {
		return nil
	}
	file := projectsDir + "/" + scope + "/" + projectFileName
	p, _ := s.lookupProject(scope)
	hidden := map[itemKey]bool{}
	warned := map[itemKey]bool{}
	var listed []string
	for _, ref := range refs {
		key, err := parseRef(ref)
		if err != nil { // parseProjectFile has already checked it
			return fmt.Errorf("%s: suppress %s: %w", file, quoteRef(ref), err)
		}
		target := s.resolvePublished(scope, key)
		switch {
		case target == nil:
			if !warned[key] {
				warned[key] = true
				s.warnings = append(s.warnings, suppressWarning(file, ref))
			}
		case target.Scope != ScopeGlobal:
			return fmt.Errorf("%s: suppress %s names the Project's own Item %s", file, quoteRef(ref), articleFile(target))
		case target.Enforced:
			return fmt.Errorf("%s: suppress names the enforced Item %s", file, articleFile(target))
		case !hidden[key]:
			hidden[key] = true
			listed = append(listed, KindArticle+":"+target.ID)
		}
	}
	sort.Strings(listed)
	p.suppressed, p.suppressions = hidden, listed
	s.projects[strings.ToLower(scope)] = p
	return nil
}

// suppressWarning says that a suppress reference names nothing. The file path
// is built from validated Project segments, so it is printable ASCII. The
// reference has passed parseRef, so, like a lookup id (quoteID), it is shown
// escaped to ASCII and cut, not redacted: article:team/gone stays readable. A
// reference that fails parseRef is a load error and goes through quoteRef.
func suppressWarning(file, ref string) string {
	w := fmt.Sprintf("%s: suppress %s names no published Item this Project can see", file, quoteID(ref))
	if len(w) > maxWarningBytes {
		w = w[:maxWarningBytes-3] + "..."
	}
	return w
}

// Suppressions lists the global Items a Project ("host/org/repo", matched
// case-insensitively) suppresses, as article:<ID> in the ID's loaded spelling,
// sorted. A reference that names nothing is not listed. An empty or
// unregistered Project suppresses nothing.
func (s *Snapshot) Suppressions(project string) []string {
	p, _ := s.lookupProject(project)
	return append([]string{}, p.suppressions...)
}

// suppressedIn returns the global Items proj hides, or nil.
func (s *Snapshot) suppressedIn(proj string) map[itemKey]bool {
	p, _ := s.lookupProject(proj)
	return p.suppressed
}
