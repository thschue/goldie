package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v3"
)

// localConfigName is the local Scope's settings file, directly in .goldie/.
const localConfigName = "config.yaml"

// localScope is the repository's own .goldie/: its Items, and the global Items
// its config.yaml suppresses. It takes part in every view (#389).
type localScope struct {
	articles []Article // sorted by ID
	agents   []Article // agent records, sorted by ID
	skills   map[string]skillBundle
	index    keyIndex
	// suppressed holds the global Items config.yaml hides in every view.
	suppressed map[itemKey]bool
	// configFile is config.yaml relative to the repository root, for errors.
	configFile string
}

// suppresses reports whether the local config.yaml hides the global Item key.
// It is safe on a nil localScope.
func (l *localScope) suppresses(key itemKey) bool { return l != nil && l.suppressed[key] }

// localSettings is the decoded .goldie/config.yaml. The client reads project,
// addr and dir; the engine ignores them but must not reject them, and any
// other key fails the load. The ignored keys are nodes so that their values
// are not interpreted.
type localSettings struct {
	Suppress yaml.Node `yaml:"suppress"`
	Project  yaml.Node `yaml:"project"`
	Addr     yaml.Node `yaml:"addr"`
	Dir      yaml.Node `yaml:"dir"`
}

// enforces reports whether the Project's own Item key is published and
// enforced, so a local Item cannot shadow it in this Project's view.
func (p project) enforces(key itemKey) bool {
	a := p.index[key]
	return a != nil && a.Enforced && a.published()
}

// LocalSource is a Source that adds the repository's own .goldie/ to another
// Source's Snapshot as the local Scope (CONTEXT: Scope).
type LocalSource struct {
	base Source
	dir  string
}

var (
	_ Source     = LocalSource{}
	_ Revisioner = LocalSource{}
)

// NewLocalSource returns a Source that loads base, then goldieDir/articles/**
// as Scope local and the suppress list of goldieDir/config.yaml. Resolution
// is local, then Project, then global. The local Scope is in every view: the
// engine is not told which Project the checkout is, so config.yaml's project,
// addr and dir are ignored, and any other key fails the load. Reading the
// directory from configuration is the caller's job.
func NewLocalSource(base Source, goldieDir string) LocalSource {
	return LocalSource{base: base, dir: goldieDir}
}

// Revision is the base Source's revision, or empty when it has none. The local
// Scope is read from the working tree, which has no revision to name.
func (s LocalSource) Revision() string {
	if r, ok := s.base.(Revisioner); ok {
		return r.Revision()
	}
	return ""
}

// Load loads the base Source and adds the local Scope to a copy of its
// Snapshot; the base Snapshot is not changed, so a base that caches its last
// Snapshot can be loaded again. A base error is returned as it is. A missing
// .goldie is an empty local Scope. An invalid file, a symlink, an Item that
// shadows an enforced Item at a wider Scope, or a reference that does not
// resolve in the local view fails the load, and the error names the file
// relative to the parent of goldieDir (.goldie/...).
func (s LocalSource) Load(ctx context.Context) (*Snapshot, error) {
	if ctx.Err() != nil {
		return nil, errLoadCancelled
	}
	base, err := s.base.Load(ctx)
	if err != nil {
		return nil, err
	}
	if base == nil {
		return nil, errors.New("the base Source returned no Snapshot")
	}
	// os.Root on the repository confines every read to it, so a symlinked
	// .goldie cannot lead out of it; contentFS never blocks on a FIFO.
	parent, name, err := splitGoldieDir(s.dir)
	if err != nil {
		return nil, err
	}
	repo, err := os.OpenRoot(parent)
	if err != nil {
		return nil, errors.New("local content directory is not readable")
	}
	defer func() { _ = repo.Close() }()
	return base.withLocal(DirSource{}.contentFS(repo), name)
}

// splitGoldieDir splits goldieDir into the repository directory and the name
// of the directory in it. Trailing separators are ignored, and doubled ones
// and inner . segments are cleaned. A path that is empty, the file system root,
// or whose last element is . or .. names no directory of its own, and cleaning
// it would read the repository itself, or the one above, as .goldie: it fails
// the load, with an error that names no path. Load does this, not
// NewLocalSource, which has no error to return.
func splitGoldieDir(dir string) (parent, name string, err error) {
	bad := errors.New("the local directory is not a named directory, such as <repository>/.goldie")
	trimmed := dir
	for len(trimmed) > 0 && os.IsPathSeparator(trimmed[len(trimmed)-1]) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if trimmed == "" {
		return "", "", bad
	}
	if last := filepath.Base(trimmed); last == "." || last == ".." || last == string(filepath.Separator) {
		return "", "", bad
	}
	clean := filepath.Clean(trimmed)
	if parent = filepath.Dir(clean); parent == clean {
		return "", "", bad
	}
	return parent, filepath.Base(clean), nil
}

// withLocal returns a copy of s with the local Scope read from dir, a path
// within fsys. It is called only while loading. The copy shares s's Projects
// and global Items, which a Snapshot never changes.
func (s *Snapshot) withLocal(fsys fs.FS, dir string) (*Snapshot, error) {
	articles, agents, skills, refs, err := readLocal(fsys, dir)
	if err != nil {
		return nil, err
	}
	snap := *s
	snap.warnings = slices.Clone(s.warnings)
	sortArticles(articles)
	sortArticles(agents)
	snap.local = &localScope{articles: articles, agents: agents, skills: skills, index: indexArticles(articles), configFile: dir + "/" + localConfigName}
	snap.local.index.add(agents)
	hidden, _, err := snap.resolveSuppressions("", snap.local.configFile, "the local Scope's", "the local Scope", refs)
	if err != nil {
		return nil, err
	}
	snap.local.suppressed = hidden
	if err := snap.checkLocalEnforced(); err != nil {
		return nil, err
	}
	own := pointers(nil, snap.local.articles, snap.local.agents)
	// Only the local Items are checked: the wider Scopes were checked, in
	// their own views, when the base loaded (ADR 0021).
	if err := snap.checkReplacements(own); err != nil {
		return nil, err
	}
	if err := snap.checkLookups(own); err != nil {
		return nil, err
	}
	return &snap, nil
}

// readLocal reads dir/articles/** and the suppress references of
// dir/config.yaml. A missing dir, articles or config.yaml is empty. Like a
// Project's articles directory and project.yaml, a symlink is refused even
// when it stays inside the root.
func readLocal(fsys fs.FS, dir string) (articles, agents []Article, skills map[string]skillBundle, refs []string, err error) {
	if exists, err := requireDir(fsys, dir, "the Goldie directory"); err != nil || !exists {
		return nil, nil, nil, nil, err
	}
	articlesPath := dir + "/articles"
	if exists, err := requireDir(fsys, articlesPath, "local articles"); err != nil {
		return nil, nil, nil, nil, err
	} else if exists {
		if articles, err = loadArticles(fsys, articlesPath, ScopeLocal); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	if agents, err = loadScopeAgents(fsys, dir+"/agents", ScopeLocal, "local agents"); err != nil {
		return nil, nil, nil, nil, err
	}
	if skills, err = loadSkills(fsys, dir+"/skills", ScopeLocal, "local skills"); err != nil {
		return nil, nil, nil, nil, err
	}
	refs, err = readSuppressFile(fsys, dir+"/"+localConfigName, func(s *localSettings) yaml.Node { return s.Suppress })
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return articles, agents, skills, refs, nil
}

// requireDir reports whether p exists, and fails when it is a symlink or not
// a directory. It opens nothing, so a FIFO cannot block it. what names p in
// the error.
func requireDir(fsys fs.FS, p, what string) (exists bool, err error) {
	fi, err := fs.Lstat(fsys, p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("%s: %w", p, readErr(err))
	case fi.Mode()&fs.ModeSymlink != 0:
		return false, fmt.Errorf("%s: is a symlink; %s must be a directory", p, what)
	case !fi.IsDir():
		return false, fmt.Errorf("%s: is not a directory; %s must be one", p, what)
	}
	return true, nil
}

// checkLocalEnforced fails when a published local Item shadows a published
// enforced Item at a wider Scope: the global one, or any Project's, since the
// local Scope answers in every Project's view (ADR 0021, CONTEXT: Enforced
// Item). A draft does not enforce and a local draft shadows nothing (#353).
// The error names both as article:<name>, never their content.
func (s *Snapshot) checkLocalEnforced() error {
	for _, a := range pointers(nil, s.local.articles, s.local.agents) {
		if !a.published() {
			continue
		}
		key := a.key()
		if s.enforces(key) {
			return localShadowErr(a, s.index[key])
		}
		for _, id := range s.Projects() {
			if p, _ := s.lookupProject(id); p.enforces(key) {
				return localShadowErr(a, p.index[key])
			}
		}
	}
	return nil
}

func localShadowErr(local, enforced *Article) error {
	return fmt.Errorf("%s: %s shadows the enforced Item %s (%s)", articleFile(local), itemRef(local), itemRef(enforced), articleFile(enforced))
}

// itemRef is a's reference, article:<ID>, with the ID escaped to ASCII and cut
// like a lookup id (quoteID), but without quotes.
func itemRef(a *Article) string {
	q := quoteID(a.ID)
	return a.Kind() + ":" + q[1:len(q)-1]
}
