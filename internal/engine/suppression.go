package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
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
// or none when there is no such file. See readSuppressFile.
func readProjectFile(fsys fs.FS, dir string) ([]string, error) {
	return readSuppressFile(fsys, dir+"/"+projectFileName, func(s *projectSettings) yaml.Node { return s.Suppress })
}

// readSuppressFile reads the settings file p and returns its suppress
// references, or none when there is no such file. The file must be a regular
// file: a symlink is rejected even when it stays inside the root, like a
// symlinked articles directory. The error names the file relative to the root.
// T is the file's settings type, which decides what keys it may hold.
func readSuppressFile[T any](fsys fs.FS, p string, suppress func(*T) yaml.Node) ([]string, error) {
	name := path.Base(p)
	fi, err := fs.Lstat(fsys, p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("%s: %w", p, readErr(err))
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("%s: is a symlink; %s must be a regular file", p, name)
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%s: is not a regular file; %s must be one", p, name)
	}
	raw, err := readLimited(fsys, p, name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	refs, err := parseSuppressFile(raw, name, suppress)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return refs, nil
}

// parseSuppressFile decodes one YAML document strictly into T: unknown keys,
// duplicate keys and a second document fail. A file with no document, only
// comments, suppresses nothing. suppress must be a list of strings, each a
// well-formed reference (ADR 0021); aliases and other node kinds fail. name
// is the file's name in an error.
func parseSuppressFile[T any](raw []byte, name string, suppress func(*T) yaml.Node) ([]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var settings T
	if err := dec.Decode(&settings); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, yamlErr(name, err)
	}
	list := suppress(&settings)
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("holds more than one YAML document")
	}
	if !present(list) {
		return nil, nil
	}
	if list.Kind != yaml.SequenceNode {
		return nil, errors.New("suppress must be a list of references of the form <kind>:<name>")
	}
	if len(list.Content) > maxSuppressions {
		return nil, fmt.Errorf("suppress has more than %d entries", maxSuppressions)
	}
	refs := make([]string, 0, len(list.Content))
	for i, n := range list.Content {
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
// every rune outside printable ASCII is escaped. name is the settings file's
// name, such as project.yaml.
func yamlErr(name string, err error) error {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	msg = strings.ReplaceAll(msg, "unmarshal errors:\n  ", "")
	msg = yamlTypeName.ReplaceAllString(msg, "")
	if utf8.RuneCountInString(msg) > maxYAMLErrorRunes {
		msg = string([]rune(msg)[:maxYAMLErrorRunes]) + "…"
	}
	q := strconv.QuoteToASCII(msg)
	return fmt.Errorf("malformed %s: %s", name, q[1:len(q)-1])
}

// applySuppressions resolves a Project's suppress references and sets its
// suppressions, in its own Resolution view. It is called only while loading,
// after the Project is added. See resolveSuppressions.
func (s *Snapshot) applySuppressions(scope string, refs []string) error {
	if len(refs) == 0 {
		return nil
	}
	file := projectsDir + "/" + scope + "/" + projectFileName
	hidden, listed, err := s.resolveSuppressions(scope, file, "the Project's", "this Project", refs)
	if err != nil {
		return err
	}
	p, _ := s.lookupProject(scope)
	p.suppressed, p.suppressions = hidden, listed
	s.projects[strings.ToLower(scope)] = p
	return nil
}

// resolveSuppressions resolves suppress references, read from file, in the
// Resolution view view by winner's rule, published Items only (#353): the
// view's published copy first, then the published global Item, and the global
// copy when it is enforced (#359). A reference to the view's own Item (owner
// names it in the error), or to an enforced global Item, fails the load. The
// caller sets the suppressions only at the end, so they never change how the
// view's own references resolve here. One that resolves to nothing, a draft
// included, is a warning, so another team's deletion never fails the view;
// viewer names it there. It returns the hidden keys, and them as
// <kind>:<ID> (article: or agent:), sorted.
func (s *Snapshot) resolveSuppressions(view, file, owner, viewer string, refs []string) (map[itemKey]bool, []string, error) {
	hidden := map[itemKey]bool{}
	warned := map[itemKey]bool{}
	var listed []string
	for _, ref := range refs {
		key, err := parseRef(ref)
		if err != nil { // the settings parser has already checked it
			return nil, nil, fmt.Errorf("%s: suppress %s: %w", file, quoteRef(ref), err)
		}
		target := s.resolvePublished(view, key)
		switch {
		case target == nil:
			if !warned[key] {
				warned[key] = true
				s.warnings = append(s.warnings, suppressWarning(file, ref, viewer))
			}
		case target.Scope != ScopeGlobal:
			return nil, nil, fmt.Errorf("%s: suppress %s names %s own Item %s", file, quoteRef(ref), owner, articleFile(target))
		case target.Enforced:
			return nil, nil, fmt.Errorf("%s: suppress names the enforced Item %s", file, articleFile(target))
		case !hidden[key]:
			hidden[key] = true
			listed = append(listed, target.Kind()+":"+target.ID)
		}
	}
	sort.Strings(listed)
	return hidden, listed, nil
}

// suppressWarning says that a suppress reference names nothing. The file path
// is built from validated Project segments, so it is printable ASCII. The
// reference has passed parseRef, so, like a lookup id (quoteID), it is shown
// escaped to ASCII and cut, not redacted: article:team/gone stays readable. A
// reference that fails parseRef is a load error and goes through quoteRef.
func suppressWarning(file, ref, viewer string) string {
	w := fmt.Sprintf("%s: suppress %s names no published Item %s can see", file, quoteID(ref), viewer)
	if len(w) > maxWarningBytes {
		w = w[:maxWarningBytes-3] + "..."
	}
	return w
}

// Suppressions lists the global Items a Project ("host/org/repo", matched
// case-insensitively) suppresses, as <kind>:<ID> (article: or agent:) in the ID's
// loaded spelling, sorted. A reference that names nothing is not listed. An empty or
// unregistered Project suppresses nothing.
func (s *Snapshot) Suppressions(project string) []string {
	p, _ := s.lookupProject(project)
	return append([]string{}, p.suppressions...)
}

// SuppressedIn reports whether a global article is hidden from proj by a
// Suppression: no copy answers in proj's view under opts (winner's rule), yet
// the Project suppresses a global article visible under opts. It answers for
// articles only; a suppressed agent is not reported here. It returns the
// Suppression as article:<ID> in the ID's loaded spelling and the project.yaml
// that sets it, relative to the Source root.
func (s *Snapshot) SuppressedIn(proj, id string, opts ...ReadOptions) (ref, file string, ok bool) {
	o := readOpts(opts)
	p, registered := s.lookupProject(proj)
	if !registered {
		return "", "", false
	}
	key := keyOf(KindArticle, id)
	if s.winner(proj, key, o) != nil || !p.suppressed[key] {
		return "", "", false
	}
	a := s.index[key]
	if a == nil || !o.visible(*a) {
		return "", "", false
	}
	return a.Kind() + ":" + a.ID, projectsDir + "/" + p.scope + "/" + projectFileName, true
}

// Warnings returns the load's findings that did not fail it, such as a suppress
// reference that names nothing, as SyncStatus.Warnings describes them. The
// caller owns the slice.
func (s *Snapshot) Warnings() []string {
	return append([]string{}, s.warnings...)
}

// suppressingFile returns proj's project.yaml, relative to the root, when
// proj suppresses the global Item key, so a reference from one of its articles
// can name why key is hidden. A draft Project copy neither shadows nor stops
// the suppression, and applySuppressions fails the load on a published one.
// The local Scope's config.yaml hides key in every view, so it is named too.
// The path is built from validated Project segments or is the local config
// file, so it is printable ASCII.
func (s *Snapshot) suppressingFile(proj string, key itemKey) (string, bool) {
	if p, ok := s.lookupProject(proj); ok && p.suppressed[key] {
		return projectsDir + "/" + p.scope + "/" + projectFileName, true
	}
	if s.local.suppresses(key) {
		return s.local.configFile, true
	}
	return "", false
}
