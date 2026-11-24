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

	"go.yaml.in/yaml/v3"
)

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
	// Overrides is set by Snapshot.View and ArticleIn on a winning Item: the
	// Scopes of the readable, lower-priority copies with the same identity, nearest first
	// (local, Project, global). Empty when the Item shadows nothing.
	Overrides []string

	// file is the article's path relative to the Source root, as
	// loadArticles walked it, for error messages.
	file string

	// kind is empty for an article. An agent is held as a record of kind
	// KindAgent, with its description and original bytes (agents.go).
	kind        string
	description string
	raw         []byte
	// order and targets are an instruction clause's; its raw is the body.
	order   int
	targets []string
	// loop is a loop record's own fields (loops.go), nil for any other Kind.
	loop *loopSpec
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

// ErrContentDirUnreadable is the load error for a content directory that is
// missing, not a directory or not readable. It never names the path. Callers
// that print it to a person add ContentDirUnreadableHint; the server's startup
// line stays as it is.
var ErrContentDirUnreadable = errors.New("content directory is not readable")

// ContentDirUnreadableHint is the next step for ErrContentDirUnreadable.
const ContentDirUnreadableHint = "check that the path you passed exists and is a readable directory"

// DirSource is a Source backed by a directory on disk.
type DirSource struct {
	root string
}

// NewDirSource returns a Source that reads articles from root. Reading the
// root from configuration is the caller's job.
func NewDirSource(root string) DirSource { return DirSource{root: root} }

// EmptySource is a Source with no content: every Load returns an empty
// Snapshot. It is the base of a local Scope over no content directory.
type EmptySource struct{}

var _ Source = EmptySource{}

// Load returns an empty Snapshot, or an error once ctx has ended.
func (EmptySource) Load(ctx context.Context) (*Snapshot, error) {
	if ctx.Err() != nil {
		return nil, errLoadCancelled
	}
	return newSnapshot(nil), nil
}

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
		return nil, ErrContentDirUnreadable
	}
	defer func() { _ = root.Close() }()
	return NewRootSource(root).Load(ctx)
}

// RootSource is a Source over a directory the caller already opened as an
// os.Root. It is DirSource without the open: a caller that validated a path
// opens it once and reads through the handle, so replacing the path with a
// symlink afterwards cannot redirect the read. The caller owns the root;
// Load never closes it.
type RootSource struct {
	root *os.Root
}

var _ Source = RootSource{}

// NewRootSource returns a Source that reads articles from root.
func NewRootSource(root *os.Root) RootSource { return RootSource{root: root} }

// Load reads the content under the root as DirSource.Load does, or fails once
// ctx has ended.
func (s RootSource) Load(ctx context.Context) (*Snapshot, error) {
	if ctx.Err() != nil {
		return nil, errLoadCancelled
	}
	return loadFS(DirSource{}.contentFS(s.root))
}

// loadFS is the loader every Source shares: it reads global/articles/**,
// projects/<host>/<org>/<repo>/articles/** and each Project's project.yaml
// from fsys into a Snapshot, and names an invalid file relative to the root of
// fsys.
func loadFS(fsys fs.FS) (*Snapshot, error) {
	global, err := loadScope(fsys, "global", ScopeGlobal, "global")
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{scopeItems: global, projects: map[string]project{}}
	dirs, err := projectDirs(fsys)
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		items, err := loadScope(fsys, d.dir, d.scope, "Project")
		if err != nil {
			return nil, err
		}
		refs, err := readProjectFile(fsys, d.dir)
		if err != nil {
			return nil, err
		}
		snap.projects[strings.ToLower(d.scope)] = project{scope: d.scope, scopeItems: items}
		if err := snap.applySuppressions(d.scope, refs); err != nil {
			return nil, err
		}
	}
	if err := snap.checkEnforced(); err != nil {
		return nil, err
	}
	if err := snap.checkReplacements(snap.allItems()); err != nil {
		return nil, err
	}
	if err := snap.checkLookups(snap.allItems()); err != nil {
		return nil, err
	}
	if err := snap.checkLoops(snap.allItems()); err != nil {
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
		raw, err := readLimited(fsys, p, "article")
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
		a.file = p
		articles = append(articles, a)
		return nil
	})
	return articles, err
}

// readLimited reads at most maxArticleBytes of a file and fails if it is larger;
// what names the file in that error, such as "article".
func readLimited(fsys fs.FS, p, what string) ([]byte, error) {
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
		return nil, fmt.Errorf("%s is larger than %d bytes", what, maxArticleBytes)
	}
	return raw, nil
}

// readErr hides why a read failed, since an OS error can carry an absolute
// path. It passes through the path-free errors about the content itself (load
// limits, invalid trees), which the operator needs to fix the Source.
func readErr(err error) error {
	for _, content := range []error{errLoadLimit, errDuplicateEntry, errBadEntryName, errNotRegular, errNotDir} {
		if errors.Is(err, content) {
			return content
		}
	}
	return errors.New("cannot read")
}

var errNoFrontmatter = errors.New("missing frontmatter (first line must be ---)")

// errArticleNoFrontmatter is the load error for an article without frontmatter. It
// names the next step and shows the minimal block, so a first article needs no
// other document.
var errArticleNoFrontmatter = errors.New("missing frontmatter (first line must be ---); an article starts with the lines: --- / name: <title> / keywords: [<word>] / ---")

// splitFrontmatter cuts a Markdown file into its frontmatter and body, reading
// CRLF as LF. raw itself is not changed.
func splitFrontmatter(raw []byte) (front, body []byte, err error) {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(raw, []byte("---\n"))
	if !ok {
		return nil, nil, errNoFrontmatter
	}
	if r, found := bytes.CutPrefix(rest, []byte("---\n")); found {
		body = r
	} else if i := bytes.Index(rest, []byte("\n---\n")); i >= 0 {
		front, body = rest[:i], rest[i+len("\n---\n"):]
	} else if f, found := bytes.CutSuffix(rest, []byte("\n---")); found {
		front = f
	} else {
		return nil, nil, errors.New("frontmatter not closed with ---")
	}
	return front, body, nil
}

func parseArticle(id string, raw []byte) (Article, error) {
	front, body, err := splitFrontmatter(raw)
	if errors.Is(err, errNoFrontmatter) {
		return Article{}, errArticleNoFrontmatter
	}
	if err != nil {
		return Article{}, err
	}
	var meta struct {
		Name          string   `yaml:"name"`
		Keywords      []string `yaml:"keywords"`
		lifecycleMeta `yaml:",inline"`
	}
	if err := yaml.Unmarshal(front, &meta); err != nil {
		return Article{}, yamlErr("frontmatter", err)
	}
	if strings.TrimSpace(meta.Name) == "" {
		return Article{}, errors.New("frontmatter has no name")
	}
	a := Article{ID: id, Name: strings.TrimSpace(meta.Name), Content: strings.TrimLeft(string(body), "\n")}
	if err := meta.apply(&a); err != nil {
		return Article{}, err
	}
	a.Keywords = normaliseKeywords(meta.Keywords)
	return a, nil
}

// lenientKeywords is the keywords of a skill or agent: the strings of a
// sequence, normalised. Anything else, a scalar or a map or a non-string
// entry, gives none, because those files never fail a load over a key the
// loader does not own.
func lenientKeywords(n yaml.Node) []string {
	if n.Kind != yaml.SequenceNode {
		return nil
	}
	var in []string
	for _, c := range n.Content {
		if v, ok := scalar(*c, "!!str"); ok {
			in = append(in, v)
		}
	}
	return normaliseKeywords(in)
}

// normaliseKeywords lower-cases and trims each keyword and drops the empty
// ones.
func normaliseKeywords(in []string) []string {
	var out []string
	for _, k := range in {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// Snapshot is the validated, immutable view of a Source. Its accessors return
// copies, so callers cannot change it.
type Snapshot struct {
	scopeItems // the global Scope's Items
	// projects maps the lower-cased "host/org/repo" to that Project.
	projects map[string]project
	// warnings are load findings that do not fail the load, such as a
	// suppress reference that names nothing. Each is safe to show.
	warnings []string
	// local is the repository's own .goldie/ (NewLocalSource), or nil. It
	// takes part in every view.
	local *localScope
	// viewProject is the Project a ViewSource's View is of, or "" (see viewFor).
	viewProject string
}

type project struct {
	scope string
	scopeItems
	// suppressed holds the global Items the Project hides (its project.yaml),
	// and suppressions lists them as <kind>:<ID> (article: or agent:), sorted.
	suppressed   map[itemKey]bool
	suppressions []string
}

// scopeItems is every Kind one Scope holds, so a Kind added later is wired in
// here once: Snapshot (global), project and localScope embed it.
type scopeItems struct {
	articles     []Article // sorted by ID
	agents       []Article // agent records, sorted by ID
	loops        []Article // loop records, sorted by ID
	instructions []Article // instruction clauses, sorted by ID
	skills       map[string]skillBundle
	index        keyIndex // articles, agents, loops and instructions by itemKey
}

// items lists pointers to the articles and agents, the Items that carry
// lookups and can shadow or replace another.
func (h scopeItems) items() []*Article { return pointers(nil, h.articles, h.agents, h.loops) }

// first returns the first Item of any Kind (article, agent, instruction) and
// the directory name of the first skill, as written; each is nil or "" for
// none. Claims use it, so every Kind is checked.
func (h scopeItems) first() (*Article, string) {
	return firstItem(h.articles, h.agents, h.loops, h.instructions), firstSkill(h.skills)
}

// loadScope reads every Kind under root, a Scope's directory in fsys. what
// names the Scope in errors ("global", "Project", "local"). Each directory goes
// through requireDir, because fs.ReadDir and fs.WalkDir follow a symlinked
// directory and os.Root only stops links out of the root. A missing directory
// is empty.
func loadScope(fsys fs.FS, root, scope, what string) (scopeItems, error) {
	var h scopeItems
	var err error
	if exists, err := requireDir(fsys, root+"/articles", what+" articles"); err != nil {
		return h, err
	} else if exists {
		if h.articles, err = loadArticles(fsys, root+"/articles", scope); err != nil {
			return h, err
		}
	}
	if h.agents, err = loadScopeAgents(fsys, root+"/agents", scope, what+" agents"); err != nil {
		return h, err
	}
	if h.loops, err = loadLoops(fsys, root, scope, what); err != nil {
		return h, err
	}
	if h.instructions, err = loadScopeKind(fsys, root+"/instructions", scope, what+" instructions", "instruction", parseClause); err != nil {
		return h, err
	}
	if h.skills, err = loadSkills(fsys, root+"/skills", scope, what+" skills"); err != nil {
		return h, err
	}
	h.index = keyIndex{}
	for _, l := range [][]Article{h.articles, h.agents, h.loops, h.instructions} {
		sortArticles(l)
		h.index.add(l)
	}
	return h, nil
}

// keyIndex maps an itemKey to its Article within one Scope. Keys are unique
// per Scope because loadArticles rejects IDs that differ only in case.
type keyIndex map[itemKey]*Article

func indexArticles(articles []Article) keyIndex {
	idx := make(keyIndex, len(articles))
	idx.add(articles)
	return idx
}

// add indexes items; an agent and an article of one name have different keys.
func (idx keyIndex) add(items []Article) {
	for i := range items {
		idx[items[i].key()] = &items[i]
	}
}

// loadScopeAgents is loadAgents behind the check that dir is not a symlink:
// fs.ReadDir follows a symlinked directory, and os.Root only stops links out
// of the root.
func loadScopeAgents(fsys fs.FS, dir, scope, what string) ([]Article, error) {
	if exists, err := requireDir(fsys, dir, what); err != nil || !exists {
		return nil, err
	}
	return loadAgents(fsys, dir, scope)
}

// loadScopeKind is loadKindFiles behind the same symlink check.
func loadScopeKind(fsys fs.FS, dir, scope, scopeWhat, what string, parse func(string, []byte) (Article, error)) ([]Article, error) {
	if exists, err := requireDir(fsys, dir, scopeWhat); err != nil || !exists {
		return nil, err
	}
	return loadKindFiles(fsys, dir, scope, what, parse)
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

// Articles returns every global article, sorted by ID, drafts included. It is
// not a reader view: it takes no ReadOptions and applies no Resolution, so a
// caller that serves readers uses Search, Article or their In forms instead.
func (s *Snapshot) Articles() []Article {
	out := make([]Article, len(s.articles))
	for i, a := range s.articles {
		out[i] = cloneArticle(a)
	}
	return out
}

// Article looks a global article up by ID, case-insensitively.
func (s *Snapshot) Article(id string, opts ...ReadOptions) (Article, bool) {
	return s.ArticleIn("", id, opts...)
}

// ArticleIn resolves an article for a Project ("host/org/repo", matched
// case-insensitively): the Project's article wins over the global one with the
// same ID. An empty or unregistered Project resolves globally. Drafts are left
// out unless opts asks for them. Which copy answers is winner's rule.
func (s *Snapshot) ArticleIn(proj, id string, opts ...ReadOptions) (Article, bool) {
	key := keyOf(KindArticle, id)
	if a := s.winner(proj, key, readOpts(opts)); a != nil {
		c := cloneArticle(*a)
		c.Overrides = s.overridden(proj, key, a)
		return c, true
	}
	return Article{}, false
}

// winner returns the copy of key that answers in proj's view under o, or nil.
// It is the one shadowing rule: every read and every load check that resolves
// an Item goes through it. proj is matched case-insensitively; an empty or
// unregistered Project is the global view. Resolution is local, then Project,
// then global (CONTEXT: Resolution).
//
//   - The local copy, which is in every view, answers when it exists, is
//     readable under o (published, or o.Drafts), and no wider copy in this
//     view is published and enforced: the global one or the Project's (ADR
//     0021, #389).
//   - Otherwise the Project's copy answers when it exists, is readable under
//     o, and the global copy is not published and enforced (#359).
//   - Otherwise the global copy answers when it exists, is readable under o,
//     and neither the Project nor the local Scope suppresses it (#360, #389).
//
// A copy that is not readable never shadows (#353). The pointer is into the
// Snapshot, so callers must not change or return it.
func (s *Snapshot) winner(proj string, key itemKey, o ReadOptions) *Article {
	p, _ := s.lookupProject(proj)
	if l := s.local; l != nil {
		if a := l.index[key]; a != nil && o.visible(*a) && !s.enforces(key) && !p.enforces(key) {
			return a
		}
	}
	if a := p.index[key]; a != nil && o.visible(*a) && !s.enforces(key) {
		return a
	}
	if a := s.index[key]; a != nil && o.visible(*a) && !p.suppressed[key] && !s.local.suppresses(key) {
		return a
	}
	return nil
}

// resolve is winner with drafts readable: the copy a replaced_by names, so
// checkReplacements can fail on a draft rather than on nothing. A Project draft
// still never shadows an enforced global Item.
func (s *Snapshot) resolve(proj string, key itemKey) *Article {
	return s.winner(proj, key, ReadOptions{Drafts: true})
}

// resolvePublished is winner in the default Resolution view: a draft is left
// out (#353), so a draft Project copy does not shadow and a draft-only Item
// resolves to nil.
func (s *Snapshot) resolvePublished(proj string, key itemKey) *Article {
	return s.winner(proj, key, ReadOptions{})
}

// overridden returns the Scopes of the copies of key that sit below the
// winning copy w in Resolution order (local, Project, global), nearest first.
// Only readable copies count: a draft is never served, so nothing is
// overridden by it.
//
// idxs lists the per-Scope indexes in the same order as resolutionOrder and
// winner read them (local, Project, global); change all three together.
func (s *Snapshot) overridden(proj string, key itemKey, w *Article) []string {
	p, _ := s.lookupProject(proj)
	idxs := []map[itemKey]*Article{p.index, s.index}
	if s.local != nil {
		idxs = append([]map[itemKey]*Article{s.local.index}, idxs...)
	}
	var below []string
	found := false
	for _, idx := range idxs {
		a := idx[key]
		switch {
		case a == nil:
		case a == w:
			found = true
		case found && (ReadOptions{}).visible(*a):
			below = append(below, a.Scope)
		}
	}
	return below
}

// resolutionOrder returns the article lists that hold proj's candidate Items:
// local, then Project, then global. winner decides which copy of each itemKey
// answers.
func (s *Snapshot) resolutionOrder(proj, kind string) [][]Article {
	pick := func(articles, agents, loops, instructions []Article) []Article {
		switch kind {
		case KindAgent:
			return agents
		case KindLoop:
			return loops
		case KindInstruction:
			return instructions
		}
		return articles
	}
	var lists [][]Article
	if s.local != nil {
		lists = append(lists, pick(s.local.articles, s.local.agents, s.local.loops, s.local.instructions))
	}
	if p, ok := s.lookupProject(proj); ok {
		lists = append(lists, pick(p.articles, p.agents, p.loops, p.instructions))
	}
	return append(lists, pick(s.articles, s.agents, s.loops, s.instructions))
}

func cloneArticle(a Article) Article {
	a.Keywords = append([]string(nil), a.Keywords...)
	a.Overrides = append([]string(nil), a.Overrides...)
	return a
}
