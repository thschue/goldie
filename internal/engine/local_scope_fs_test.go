package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #389, criterion 4: .goldie, .goldie/articles and .goldie/config.yaml
// come from the checked-out repository, so they are untrusted. They are opened
// through os.Root and a symlink is refused; the article and config.yaml size
// caps and the case-insensitive ids apply as for any Source. Every error names
// the path relative to the repository root and never a host path. A missing
// .goldie, an empty one, and one with only config.yaml all load cleanly.

// localFSSource returns the local Source over a base holding one global
// article, with a repository directory that setup prepares.
func localFSSource(t *testing.T, setup func(t *testing.T, repo string) error) (engine.Source, localDirs, bool) {
	t.Helper()
	dirs := localDirs{
		content: writeFiles(t, map[string]string{globalFile("style"): article("Global style", "rollout")}),
		repo:    t.TempDir(),
	}
	if err := setup(t, dirs.repo); err != nil {
		return nil, dirs, false
	}
	return engine.NewLocalSource(engine.NewDirSource(dirs.content), filepath.Join(dirs.repo, ".goldie")), dirs, true
}

// outsideRepo is a directory outside the repository holding what a symlink
// would smuggle in: an article and a config.yaml.
func outsideRepo(t *testing.T) string {
	t.Helper()
	return writeFiles(t, map[string]string{
		"articles/stolen.md": article("Stolen", "rollout"),
		"stolen.md":          article("Stolen", "rollout"),
		"config.yaml":        suppressYAML("article:style"),
	})
}

// assertRefused fails unless Load failed with an error that names want and no
// host path, and served nothing.
func assertRefused(t *testing.T, src engine.Source, dirs localDirs, outside, want string) {
	t.Helper()
	snap, err := src.Load(t.Context())
	if err == nil {
		_, stolen := snap.Article("stolen")
		t.Fatalf("Load accepted it (stolen article served: %v)", stolen)
	}
	if snap != nil {
		t.Error("a failed Load returned a Snapshot")
	}
	msg := err.Error()
	if !strings.Contains(msg, want) {
		t.Errorf("error %q does not name %s", msg, want)
	}
	assertNoHostPath(t, msg, dirs)
	if outside != "" && strings.Contains(msg, outside) {
		t.Errorf("error %q leaks the symlink target %s", msg, outside)
	}
}

func symlink(t *testing.T, target, link string) error {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	return os.Symlink(target, link)
}

func TestSymlinkedGoldieDirectoryIsRefused(t *testing.T) {
	outside := outsideRepo(t)
	for name, setup := range map[string]func(t *testing.T, repo string) error{
		"outside the repository": func(t *testing.T, repo string) error {
			return symlink(t, outside, filepath.Join(repo, ".goldie"))
		},
		"inside the repository": func(t *testing.T, repo string) error {
			writeTo(t, repo, "docs/goldie/articles/stolen.md", article("Stolen"))
			return symlink(t, "docs/goldie", filepath.Join(repo, ".goldie"))
		},
		"dangling": func(t *testing.T, repo string) error {
			return symlink(t, "missing", filepath.Join(repo, ".goldie"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			src, dirs, ok := localFSSource(t, setup)
			if !ok {
				t.Skip("symlinks unavailable")
			}
			assertRefused(t, src, dirs, outside, ".goldie")
		})
	}
}

func TestGoldieThatIsNotADirectoryIsRefused(t *testing.T) {
	src, dirs, _ := localFSSource(t, func(t *testing.T, repo string) error {
		writeTo(t, repo, ".goldie", "articles: here\n")
		return nil
	})
	assertRefused(t, src, dirs, "", ".goldie")
}

func TestSymlinkedLocalArticlesDirectoryIsRefused(t *testing.T) {
	outside := outsideRepo(t)
	for name, setup := range map[string]func(t *testing.T, repo string) error{
		"outside the repository": func(t *testing.T, repo string) error {
			return symlink(t, filepath.Join(outside, "articles"), filepath.Join(repo, ".goldie", "articles"))
		},
		"inside .goldie": func(t *testing.T, repo string) error {
			writeTo(t, repo, ".goldie/other/stolen.md", article("Stolen"))
			return symlink(t, "other", filepath.Join(repo, ".goldie", "articles"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			src, dirs, ok := localFSSource(t, setup)
			if !ok {
				t.Skip("symlinks unavailable")
			}
			assertRefused(t, src, dirs, outside, ".goldie/articles")
		})
	}
}

func TestLocalConfigThatIsASymlinkOrDirectoryIsRefused(t *testing.T) {
	outside := outsideRepo(t)
	for name, setup := range map[string]func(t *testing.T, repo string) error{
		"symlink outside the repository": func(t *testing.T, repo string) error {
			return symlink(t, filepath.Join(outside, "config.yaml"), filepath.Join(repo, filepath.FromSlash(localConfigFile)))
		},
		"symlink inside .goldie": func(t *testing.T, repo string) error {
			writeTo(t, repo, ".goldie/shared.yaml", suppressYAML("article:style"))
			return symlink(t, "shared.yaml", filepath.Join(repo, filepath.FromSlash(localConfigFile)))
		},
		"dangling symlink": func(t *testing.T, repo string) error {
			return symlink(t, "missing.yaml", filepath.Join(repo, filepath.FromSlash(localConfigFile)))
		},
		"directory": func(t *testing.T, repo string) error {
			writeTo(t, repo, localConfigFile+"/suppress.yaml", suppressYAML("article:style"))
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			src, dirs, ok := localFSSource(t, setup)
			if !ok {
				t.Skip("symlinks unavailable")
			}
			assertRefused(t, src, dirs, outside, localConfigFile)
		})
	}
}

// A symlinked article file, or a symlinked directory within articles, is never
// followed: Load refuses it, or loads without it. Either way nothing it points
// at is served, and an error names it relative to the repository.
func TestSymlinkedLocalArticleIsNotFollowed(t *testing.T) {
	outside := outsideRepo(t)
	for name, tt := range map[string]struct {
		place  string
		target string // relative to the link's directory, or absolute
	}{
		"file outside the repository":      {localFile("stolen"), filepath.Join(outside, "stolen.md")},
		"file inside the repository":       {localFile("stolen"), "../../docs/stolen.md"},
		"directory outside the repository": {".goldie/articles/team", filepath.Join(outside, "articles")},
	} {
		t.Run(name, func(t *testing.T) {
			src, dirs, ok := localFSSource(t, func(t *testing.T, repo string) error {
				writeTo(t, repo, localFile("mine"), article("Mine"))
				writeTo(t, repo, "docs/stolen.md", article("Stolen", "rollout"))
				return symlink(t, tt.target, filepath.Join(repo, filepath.FromSlash(tt.place)))
			})
			if !ok {
				t.Skip("symlinks unavailable")
			}
			snap, err := src.Load(t.Context())
			if err != nil {
				if !strings.Contains(err.Error(), tt.place) {
					t.Errorf("error %q does not name %s", err, tt.place)
				}
				assertNoHostPath(t, err.Error(), dirs)
				if strings.Contains(err.Error(), outside) {
					t.Errorf("error %q leaks the symlink target", err)
				}
				return
			}
			for _, id := range []string{"stolen", "team/stolen"} {
				for _, proj := range localViews {
					if a, ok := snap.ArticleIn(proj, id, withDrafts); ok {
						t.Errorf("ArticleIn(%q, %q) = %+v; a symlinked article must not be served", proj, id, a)
					}
				}
			}
			for _, h := range snap.Search("rollout", withDrafts) {
				if h.Name == "Stolen" {
					t.Errorf("Search served the symlinked article %s", h.ID)
				}
			}
		})
	}
}

func TestLocalArticleLargerThanTheCapFailsTheLoad(t *testing.T) {
	const capBytes = 1 << 20
	pad := func(total int) string {
		head := "---\nname: Big\n---\n"
		return head + strings.Repeat("x", total-len(head))
	}
	msg := loadLocalErr(t, map[string]string{localFile("big"): pad(capBytes + 1)}, localFile("big"))
	if len(msg) > 1<<10 {
		t.Errorf("error is %d bytes; it must not carry the content", len(msg))
	}
	snap := loadLocal(t, map[string]string{localFile("big"): pad(capBytes)})
	if got := answer(snap, "", "big"); got != engine.ScopeLocal+"/Big" {
		t.Errorf("an article of exactly the cap answered %s; want it served", got)
	}
}

func TestLocalConfigLargerThanTheCapFailsTheLoad(t *testing.T) {
	const capBytes = 1 << 20
	pad := func(total int) string {
		head := "suppress: [article:style]\n#"
		return head + strings.Repeat("x", total-len(head)-1) + "\n"
	}
	loadLocalErr(t, map[string]string{globalFile("style"): article("Style"), localConfigFile: pad(capBytes + 1)}, localConfigFile)
	snap := loadLocal(t, map[string]string{globalFile("style"): article("Style"), localConfigFile: pad(capBytes)})
	if got := answer(snap, "", "style"); got != "none" {
		t.Errorf("a config.yaml of exactly the cap was not applied: style answered %s", got)
	}
}

func TestLocalIDsDifferingOnlyInCaseFailTheLoad(t *testing.T) {
	for name, pair := range map[string][2]string{
		"same directory": {localFile("Postgres"), localFile("postgres")},
		"nested":         {localFile("Db/Setup"), localFile("db/setup")},
	} {
		t.Run(name, func(t *testing.T) {
			loadLocalErr(t, map[string]string{pair[0]: article("One"), pair[1]: article("Two")}, pair[0], pair[1])
		})
	}
}

// A local id is matched ignoring case, against its own Scope and the wider
// ones (ADR 0015).
func TestLocalIDsMatchIgnoringCase(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalFile("platform/deploy"): article("Global deploy"),
		localFile("Platform/Deploy"):  article("Local deploy"),
	})
	for _, id := range []string{"platform/deploy", "PLATFORM/DEPLOY", "Platform/Deploy"} {
		a, ok := snap.ArticleIn("", id)
		if !ok || a.Scope != engine.ScopeLocal || a.ID != "Platform/Deploy" {
			t.Errorf("ArticleIn(%q) = %+v, %v; want the local Item, ID in its loaded spelling", id, a, ok)
		}
	}
}

// A local article is validated as any article is, and the error names it
// relative to the repository.
func TestInvalidLocalArticleFailsTheLoad(t *testing.T) {
	for name, content := range map[string]string{
		"no frontmatter":   "Just text.\n",
		"unclosed":         "---\nname: X\n",
		"no name":          "---\nkeywords: [a]\n---\nBody.\n",
		"bad status":       govArticle("X", "status: archived"),
		"bad enforced":     govArticle("X", "enforced: yes"),
		"bad replaced_by":  govArticle("X", "replaced_by: style"),
		"malformed YAML":   "---\nname: [x\n---\nBody.\n",
		"hostile name key": "---\n\"\\u202ename\": x\n---\nBody.\n",
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadLocalErr(t, map[string]string{localFile("team/bad"): content}, localFile("team/bad"))
			assertNoFormatRunes(t, msg)
		})
	}
}

// --- Missing, empty and config-only .goldie directories load cleanly ---

func TestGoldieWithoutArticlesLoadsCleanly(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, repo string) error{
		"missing .goldie": func(*testing.T, string) error { return nil },
		"empty .goldie": func(t *testing.T, repo string) error {
			mustMkdir(t, filepath.Join(repo, ".goldie"))
			return nil
		},
		"empty articles": func(t *testing.T, repo string) error {
			mustMkdir(t, filepath.Join(repo, ".goldie", "articles"))
			return nil
		},
		"only config.yaml with project": func(t *testing.T, repo string) error {
			writeTo(t, repo, localConfigFile, "project: "+govProject+"\n")
			return nil
		},
		"only an empty config.yaml": func(t *testing.T, repo string) error {
			writeTo(t, repo, localConfigFile, "")
			return nil
		},
		"non-Markdown files only": func(t *testing.T, repo string) error {
			writeTo(t, repo, ".goldie/articles/README.txt", "not an article")
			writeTo(t, repo, ".goldie/notes.md", article("Not in articles"))
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			src, _, _ := localFSSource(t, setup)
			s := engine.NewSyncer(src)
			if err := s.Poll(t.Context()); err != nil {
				t.Fatalf("Poll: %v", err)
			}
			st := s.Status()
			if st.LastError != "" || len(syncWarnings(t, st)) != 0 {
				t.Errorf("Status = %+v; want a clean load", st)
			}
			snap := s.Current()
			for _, proj := range localViews {
				if got := answer(snap, proj, "style"); got != engine.ScopeGlobal+"/Global style" {
					t.Errorf("ArticleIn(%q, style) answered %s; the base content must answer", proj, got)
				}
				if got := answer(snap, proj, "notes"); got != "none" {
					t.Errorf("ArticleIn(%q, notes) answered %s; only .goldie/articles holds local Items", proj, got)
				}
			}
		})
	}
}
