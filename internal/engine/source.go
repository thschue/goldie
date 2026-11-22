package engine

import (
	"bytes"
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

// articlesDir is where a Source keeps its global articles, relative to its root.
const articlesDir = "global/articles"

// maxArticleBytes caps one article file, so a Source cannot make Load read
// without bound.
const maxArticleBytes = 1 << 20

// Article is one global Item of Kind article: a Markdown file with frontmatter.
type Article struct {
	// ID is the file's path under global/articles without ".md", slash separated.
	ID string
	// Name is the frontmatter name.
	Name string
	// Keywords are lower-cased and trimmed.
	Keywords []string
	// Content is the Markdown body after the frontmatter.
	Content string
}

// DirSource is a Source backed by a directory on disk.
type DirSource struct {
	root string
}

// NewDirSource returns a Source that reads articles from root. Reading the
// root from configuration is the caller's job.
func NewDirSource(root string) DirSource { return DirSource{root: root} }

// Load reads global/articles/** under the root into a Snapshot. It fails on the
// first invalid file, and the error names that file relative to the root. A
// root without global/articles yields an empty Snapshot.
func (s DirSource) Load() (*Snapshot, error) {
	info, err := os.Stat(s.root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("content directory is not readable")
	}
	fsys := os.DirFS(s.root)
	var articles []Article
	err = fs.WalkDir(fsys, articlesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == articlesDir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return fmt.Errorf("%s: cannot read", p)
		}
		if !d.Type().IsRegular() || path.Ext(p) != ".md" {
			return nil
		}
		raw, err := readLimited(fsys, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		id := strings.TrimSuffix(strings.TrimPrefix(p, articlesDir+"/"), ".md")
		a, err := parseArticle(id, raw)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		articles = append(articles, a)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return newSnapshot(articles), nil
}

// readLimited reads at most maxArticleBytes of a file and fails if it is larger.
func readLimited(fsys fs.FS, p string) ([]byte, error) {
	f, err := fsys.Open(p)
	if err != nil {
		return nil, errors.New("cannot read")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxArticleBytes+1))
	if err != nil {
		return nil, errors.New("cannot read")
	}
	if len(raw) > maxArticleBytes {
		return nil, fmt.Errorf("article is larger than %d bytes", maxArticleBytes)
	}
	return raw, nil
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
		Name     string   `yaml:"name"`
		Keywords []string `yaml:"keywords"`
	}
	if err := yaml.Unmarshal(front, &meta); err != nil {
		return Article{}, fmt.Errorf("malformed frontmatter: %s", strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if strings.TrimSpace(meta.Name) == "" {
		return Article{}, errors.New("frontmatter has no name")
	}
	a := Article{ID: id, Name: strings.TrimSpace(meta.Name), Content: strings.TrimLeft(string(body), "\n")}
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
	articles []Article // sorted by ID
}

func newSnapshot(articles []Article) *Snapshot {
	sort.Slice(articles, func(i, j int) bool { return articles[i].ID < articles[j].ID })
	return &Snapshot{articles: articles}
}

// Articles returns every article, sorted by ID.
func (s *Snapshot) Articles() []Article {
	out := make([]Article, len(s.articles))
	for i, a := range s.articles {
		out[i] = cloneArticle(a)
	}
	return out
}

// Article looks an article up by ID, case-insensitively.
func (s *Snapshot) Article(id string) (Article, bool) {
	id = strings.ToLower(id)
	for _, a := range s.articles {
		if strings.ToLower(a.ID) == id {
			return cloneArticle(a), true
		}
	}
	return Article{}, false
}

func cloneArticle(a Article) Article {
	a.Keywords = append([]string(nil), a.Keywords...)
	return a
}
