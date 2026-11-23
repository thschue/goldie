package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v4"
)

// articlesDir is where a Source keeps its global articles, relative to its root.
const articlesDir = "global/articles"

// projectsDir holds Project scope: projects/<host>/<org>/<repo>/articles/**.
const projectsDir = "projects"

// maxProjectIDLen is the maxLength of the ProjectId schema in the spec. The
// engine cannot import the contract, so loading holds a Source to it here; a
// longer id would be listed but could never be selected with project=.
const maxProjectIDLen = 512

// maxArticleBytes caps one article file, so a Source cannot make Load read
// without bound.
const maxArticleBytes = 1 << 20

// errLoadLimit is what an fs.FS returns, or wraps, once a load runs past its
// limits on work or content read, for example a treeFS out of budget.
var errLoadLimit = errors.New("content exceeds the load limits")

// errLoadCancelled is what a Load returns when its context ended before or
// while it ran.
var errLoadCancelled = errors.New("the load was cancelled")

// Article is one global Item of Kind article: a Markdown file with frontmatter.
type Article struct {
	// ID is the article's identity: its path under <scope>/articles/ without
	// ".md", slash separated. IDs compare case-insensitively (ADR 0015).
	ID string
	// Scope is ScopeGlobal, or the Project's "host/org/repo".
	Scope string
	// Name is the frontmatter name, the display title. It is not the identity.
	Name string
	// Keywords are lower-cased and trimmed.
	Keywords []string
	// Content is the Markdown body after the frontmatter.
	Content string
	// Status is StatusDraft, StatusPublished (the default) or StatusDeprecated.
	Status string
	// Enforced is the frontmatter enforced: true (CONTEXT: Enforced Item).
	Enforced bool
	// ReplacedBy is the frontmatter replaced_by as written, a reference
	// <kind>:<name> that resolves in this Item's view (ADR 0021), or empty.
	ReplacedBy string
}

// Source produces a Snapshot. DirSource is one; a git Source is another. A
// Source may also implement Revisioner. Load stops, with an error, once ctx
// ends; a Load whose ctx has already ended loads nothing.
type Source interface {
	Load(ctx context.Context) (*Snapshot, error)
}

// Revisioner is implemented by a Source that can name the revision its last
// successful Load read. The value is opaque to the engine. DirSource does not
// implement it, so its Revision is empty.
type Revisioner interface {
	Revision() string
}

var _ Source = DirSource{}

// DirSource is a Source backed by a directory on disk.
type DirSource struct {
	root string
}

// NewDirSource returns a Source that reads articles from root. Reading the
// root from configuration is the caller's job.
func NewDirSource(root string) DirSource { return DirSource{root: root} }

// Load reads global/articles/** and projects/<host>/<org>/<repo>/articles/**
// under the root into a Snapshot. It fails on the first invalid file or Project
// path, and the error names it relative to the root. A root without either
// yields an empty Snapshot. Any projects/<host>/<org>/<repo>/ directory
// registers that Project, with or without articles. A local read is short, so
// ctx is checked only before it starts.
func (s DirSource) Load(ctx context.Context) (*Snapshot, error) {
	if ctx.Err() != nil {
		return nil, errLoadCancelled
	}
	// os.Root confines every Stat, ReadDir and Open to the root, so a symlinked
	// directory cannot lead the walk out of it.
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, errors.New("content directory is not readable")
	}
	defer func() { _ = root.Close() }()
	return loadFS(root.FS())
}

// loadFS is the loader every Source shares: it reads global/articles/** and
// projects/<host>/<org>/<repo>/articles/** from fsys into a Snapshot, and names
// an invalid file relative to the root of fsys.
func loadFS(fsys fs.FS) (*Snapshot, error) {
	global, err := loadArticles(fsys, articlesDir, ScopeGlobal)
	if err != nil {
		return nil, err
	}
	snap := newSnapshot(global)
	dirs, err := projectDirs(fsys)
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		// fs.WalkDir follows a symlinked start directory, and os.Root only
		// stops links out of the root, so reject a symlinked articles dir here.
		dir := d.dir + "/articles"
		if fi, err := fs.Lstat(fsys, dir); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s: is a symlink; Project articles must be a directory", dir)
		}
		articles, err := loadArticles(fsys, dir, d.scope)
		if err != nil {
			return nil, err
		}
		snap.addProject(d.scope, articles)
	}
	if err := snap.checkReplacements(); err != nil {
		return nil, err
	}
	return snap, nil
}

type projectDir struct{ dir, scope string }

// projectDirs lists projects/<host>/<org>/<repo> directories. Anything else at
// those depths (a file, a symlink, a bad host name) is a validation error.
func projectDirs(fsys fs.FS) ([]projectDir, error) {
	var out []projectDir
	seen := map[string]string{} // lower-cased scope -> directory
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			if dir == projectsDir && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("%s: %w", dir, readErr(err))
		}
		for _, e := range entries {
			p := dir + "/" + e.Name()
			if !e.IsDir() {
				return fmt.Errorf("%s: not a directory; Projects live at projects/<host>/<org>/<repo>", p)
			}
			if err := validProjectSegment(depth, e.Name()); err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			if depth < 2 {
				if err := walk(p, depth+1); err != nil {
					return err
				}
				continue
			}
			scope := strings.TrimPrefix(p, projectsDir+"/")
			if len(scope) > maxProjectIDLen {
				return fmt.Errorf("%s: the Project id is longer than %d bytes", p, maxProjectIDLen)
			}
			if other, dup := seen[strings.ToLower(scope)]; dup {
				return fmt.Errorf("%s and %s: Projects differ only in case", other, p)
			}
			seen[strings.ToLower(scope)] = p
			out = append(out, projectDir{dir: p, scope: scope})
		}
		return nil
	}
	if err := walk(projectsDir, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// validProjectSegment checks one path segment: depth 0 is the host (lower-case
// DNS name), 1 the org and 2 the repo.
func validProjectSegment(depth int, name string) error {
	if depth == 0 {
		if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789.-") != "" || name[0] == '.' || name[0] == '-' {
			return errors.New("invalid Project host: use a lower-case host name such as github.com")
		}
		return nil
	}
	what := "org"
	if depth == 2 {
		what = "repo"
	}
	if name == "" || strings.HasPrefix(name, ".") || strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") != "" {
		return fmt.Errorf("invalid Project %s: use letters, digits, '.', '_' and '-'", what)
	}
	return nil
}

// loadArticles reads every .md file under dir into Articles of the given scope.
// A missing dir yields none.
func loadArticles(fsys fs.FS, dir, scope string) ([]Article, error) {
	var articles []Article
	seen := map[string]string{} // lower-cased ID -> file path
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return fmt.Errorf("%s: %w", p, readErr(err))
		}
		if !d.Type().IsRegular() || path.Ext(p) != ".md" {
			return nil
		}
		raw, err := readLimited(fsys, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		id := strings.TrimSuffix(strings.TrimPrefix(p, dir+"/"), ".md")
		if other, dup := seen[strings.ToLower(id)]; dup {
			return fmt.Errorf("%s and %s: article IDs differ only in case", other, p)
		}
		seen[strings.ToLower(id)] = p
		a, err := parseArticle(id, raw)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		a.Scope = scope
		articles = append(articles, a)
		return nil
	})
	return articles, err
}

// readLimited reads at most maxArticleBytes of a file and fails if it is larger.
func readLimited(fsys fs.FS, p string) ([]byte, error) {
	f, err := fsys.Open(p)
	if err != nil {
		return nil, readErr(err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxArticleBytes+1))
	if err != nil {
		return nil, readErr(err)
	}
	if len(raw) > maxArticleBytes {
		return nil, fmt.Errorf("article is larger than %d bytes", maxArticleBytes)
	}
	return raw, nil
}

// readErr hides why a read failed, since an OS error can carry an absolute
// path. It passes through the path-free errors about the content itself (load
// limits, invalid trees), which the operator needs to fix the Source.
func readErr(err error) error {
	for _, content := range []error{errLoadLimit, errDuplicateEntry, errBadEntryName} {
		if errors.Is(err, content) {
			return content
		}
	}
	return errors.New("cannot read")
}

func parseArticle(id string, raw []byte) (Article, error) {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(raw, []byte("---\n"))
	if !ok {
		return Article{}, errors.New("missing frontmatter (first line must be ---)")
	}
	var front, body []byte
	if r, found := bytes.CutPrefix(rest, []byte("---\n")); found {
		body = r
	} else if i := bytes.Index(rest, []byte("\n---\n")); i >= 0 {
		front, body = rest[:i], rest[i+len("\n---\n"):]
	} else if f, found := bytes.CutSuffix(rest, []byte("\n---")); found {
		front = f
	} else {
		return Article{}, errors.New("frontmatter not closed with ---")
	}
	var meta struct {
		Name          string   `yaml:"name"`
		Keywords      []string `yaml:"keywords"`
		lifecycleMeta `yaml:",inline"`
	}
	if err := yaml.Unmarshal(front, &meta); err != nil {
		return Article{}, fmt.Errorf("malformed frontmatter: %s", strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if strings.TrimSpace(meta.Name) == "" {
		return Article{}, errors.New("frontmatter has no name")
	}
	a := Article{ID: id, Name: strings.TrimSpace(meta.Name), Content: strings.TrimLeft(string(body), "\n")}
	if err := meta.apply(&a); err != nil {
		return Article{}, err
	}
	for _, k := range meta.Keywords {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			a.Keywords = append(a.Keywords, k)
		}
	}
	return a, nil
}

// Snapshot is the validated, immutable view of a Source. Its accessors return
// copies, so callers cannot change it.
type Snapshot struct {
	articles []Article // global, sorted by ID
	index    keyIndex  // global articles by itemKey
	// projects maps the lower-cased "host/org/repo" to that Project.
	projects map[string]project
}

type project struct {
	scope    string
	articles []Article // sorted by ID
	index    keyIndex
}

// keyIndex maps an itemKey to its Article within one Scope. Keys are unique
// per Scope because loadArticles rejects IDs that differ only in case.
type keyIndex map[itemKey]*Article

func indexArticles(articles []Article) keyIndex {
	idx := make(keyIndex, len(articles))
	for i := range articles {
		idx[articles[i].key()] = &articles[i]
	}
	return idx
}

func newSnapshot(articles []Article) *Snapshot {
	sortArticles(articles)
	return &Snapshot{articles: articles, index: indexArticles(articles), projects: map[string]project{}}
}

func sortArticles(a []Article) {
	sort.Slice(a, func(i, j int) bool { return a[i].ID < a[j].ID })
}

// addProject registers a Project. It is called only while loading.
func (s *Snapshot) addProject(scope string, articles []Article) {
	sortArticles(articles)
	s.projects[strings.ToLower(scope)] = project{scope: scope, articles: articles, index: indexArticles(articles)}
}

// Projects lists the registered Projects as "host/org/repo", sorted. A Project
// is registered by its directory, whether or not it has articles.
func (s *Snapshot) Projects() []string {
	out := make([]string, 0, len(s.projects))
	for _, p := range s.projects {
		out = append(out, p.scope)
	}
	sort.Strings(out)
	return out
}

// Project reports whether id ("host/org/repo") is a registered Project,
// matched case-insensitively, and returns it in its registered spelling.
// lookupProject is the one place that decides whether a Project is registered;
// this method and article resolution both go through it. The id length cap
// applies only when loading, not here.
func (s *Snapshot) Project(id string) (scope string, ok bool) {
	p, ok := s.lookupProject(id)
	return p.scope, ok
}

// lookupProject finds a registered Project by id, case-insensitively.
func (s *Snapshot) lookupProject(id string) (project, bool) {
	if id == "" {
		return project{}, false
	}
	p, ok := s.projects[strings.ToLower(id)]
	return p, ok
}

// Articles returns every global article, sorted by ID.
func (s *Snapshot) Articles() []Article {
	out := make([]Article, len(s.articles))
	for i, a := range s.articles {
		out[i] = cloneArticle(a)
	}
	return out
}

// Article looks a global article up by ID, case-insensitively.
func (s *Snapshot) Article(id string) (Article, bool) {
	return s.ArticleIn("", id)
}

// ArticleIn resolves an article for a Project ("host/org/repo", matched
// case-insensitively): the Project's article wins over the global one with the
// same ID. An empty or unregistered Project resolves globally.
func (s *Snapshot) ArticleIn(proj, id string) (Article, bool) {
	if a := s.resolve(proj, keyOf(KindArticle, id)); a != nil {
		return cloneArticle(*a), true
	}
	return Article{}, false
}

// resolve returns the Item that wins for key in proj's view, Project before
// global, or nil. The pointer is into the Snapshot, so callers must not change
// or return it.
func (s *Snapshot) resolve(proj string, key itemKey) *Article {
	if p, ok := s.lookupProject(proj); ok {
		if a := p.index[key]; a != nil {
			return a
		}
	}
	return s.index[key]
}

// resolutionOrder returns the article lists to consult, Project before global.
// An Item in an earlier list shadows one with the same itemKey in a later one.
func (s *Snapshot) resolutionOrder(proj string) [][]Article {
	if p, ok := s.lookupProject(proj); ok {
		return [][]Article{p.articles, s.articles}
	}
	return [][]Article{s.articles}
}

func cloneArticle(a Article) Article {
	a.Keywords = append([]string(nil), a.Keywords...)
	return a
}
