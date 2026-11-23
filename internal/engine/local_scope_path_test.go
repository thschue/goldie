package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #389, review finding on PR #397: goldieDir is the path to the .goldie
// directory, and how it is spelled must not change what loads. A trailing or
// doubled separator names the same directory, so it loads exactly as the
// clean path does; it must never silently yield an empty local Scope, which
// would drop the local suppressions, the enforced-shadow check and the
// reference checks. A path that does not name a directory inside a repository
// ("", ".", "..", the file system root, or one ending in . or ..) fails the
// load with an error that names no host path, because reading the repository
// itself, or its parent, as .goldie is never what the caller meant.

// goldieSpellings are ways to write repo/.goldie that name the same directory.
func goldieSpellings(repo string) map[string]string {
	sep := string(filepath.Separator)
	clean := filepath.Join(repo, ".goldie")
	return map[string]string{
		"clean":              clean,
		"trailing slash":     clean + sep,
		"doubled trailing":   clean + sep + sep,
		"doubled inside":     repo + sep + sep + ".goldie" + sep,
		"dot segment before": repo + sep + "." + sep + ".goldie" + sep,
	}
}

func TestGoldieDirSpellingDoesNotChangeTheLocalScope(t *testing.T) {
	content := writeFiles(t, map[string]string{
		globalFile("style"): article("Global style"),
		globalFile("kept"):  article("Kept"),
	})
	repo := writeFiles(t, map[string]string{
		localFile("mine"):  article("Mine"),
		localFile("style"): article("Local style"),
		localConfigFile:    suppressYAML("article:kept"),
	})
	for name, dir := range goldieSpellings(repo) {
		t.Run(name, func(t *testing.T) {
			snap, err := engine.NewLocalSource(engine.NewDirSource(content), dir).Load(t.Context())
			if err != nil {
				t.Fatalf("Load(%q): %v", dir, err)
			}
			for _, proj := range localViews {
				for id, want := range map[string]string{
					"mine":  engine.ScopeLocal + "/Mine",
					"style": engine.ScopeLocal + "/Local style",
					"kept":  "none",
				} {
					if got := answer(snap, proj, id); got != want {
						t.Errorf("goldieDir %q: ArticleIn(%q, %q) answered %s; want %s, as for the clean path", dir, proj, id, got, want)
					}
				}
			}
		})
	}
}

func TestGoldieDirSpellingStillFailsOnAnEnforcedShadow(t *testing.T) {
	content := writeFiles(t, map[string]string{globalFile("rules"): govArticle("Rules", "enforced: true")})
	repo := writeFiles(t, map[string]string{localFile("rules"): article("Local rules")})
	dirs := localDirs{content: content, repo: repo}
	for name, dir := range goldieSpellings(repo) {
		t.Run(name, func(t *testing.T) {
			snap, err := engine.NewLocalSource(engine.NewDirSource(content), dir).Load(t.Context())
			if err == nil {
				t.Fatalf("goldieDir %q: Load succeeded (rules answered %s); a local Item shadowing an enforced Item must fail it",
					dir, answer(snap, "", "rules"))
			}
			if !strings.Contains(err.Error(), "article:rules") {
				t.Errorf("goldieDir %q: error %q does not name article:rules", dir, err)
			}
			assertNoHostPath(t, err.Error(), dirs)
		})
	}
}

// A goldieDir that names no .goldie directory inside a repository fails the
// load. Load must neither read the repository (or the working directory, or
// the file system root) as .goldie nor treat it as missing.
func TestDegenerateGoldieDirFailsTheLoad(t *testing.T) {
	content := writeFiles(t, map[string]string{globalFile("style"): article("Global style")})
	// The repository holds articles/ and config.yaml at its top, so reading it
	// as .goldie would load something.
	repo := writeFiles(t, map[string]string{
		"articles/stolen.md":         article("Stolen"),
		"config.yaml":                suppressYAML("article:style"),
		".goldie/articles/mine.md":   article("Mine"),
		"sub/.goldie/articles/x.md":  article("X"),
		"sub/articles/stolen-two.md": article("Stolen two"),
	})
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	root := filepath.VolumeName(repo) + sep
	for name, dir := range map[string]string{
		"empty":                  "",
		"dot":                    ".",
		"dot-dot":                "..",
		"dot with slash":         "." + sep,
		"file system root":       root,
		"repository then dot":    repo + sep + ".",
		"repository then dotdot": filepath.Join(repo, "sub") + sep + "..",
		"goldie then dotdot":     filepath.Join(repo, ".goldie") + sep + "..",
		"goldie then dotdot/":    filepath.Join(repo, ".goldie") + sep + ".." + sep,
	} {
		t.Run(name, func(t *testing.T) {
			snap, err := engine.NewLocalSource(engine.NewDirSource(content), dir).Load(t.Context())
			if err == nil {
				t.Fatalf("goldieDir %q: Load succeeded (stolen: %s, style: %s); want it refused",
					dir, answer(snap, "", "stolen"), answer(snap, "", "style"))
			}
			if snap != nil {
				t.Errorf("goldieDir %q: a failed Load returned a Snapshot", dir)
			}
			msg := err.Error()
			assertNoHostPath(t, msg, localDirs{content: content, repo: repo})
			if strings.Contains(msg, cwd) {
				t.Errorf("goldieDir %q: error %q leaks the working directory", dir, msg)
			}
		})
	}
}
