package engine_test

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// A SKILL.md is parsed as YAML, so it is held to one article's size, not the
// bundle's; other files in the bundle keep the bundle cap.
func TestSkillMdIsHeldToOneMiB(t *testing.T) {
	big := "---\nname: x\ndescription: d\nk: " + strings.Repeat("a", 1<<20) + "\n---\n"
	files := map[string]string{globalSkillFile("x", "SKILL.md"): big}

	check := func(t *testing.T, src engine.Source) {
		t.Helper()
		_, err := src.Load(t.Context())
		if err == nil || !strings.Contains(err.Error(), "SKILL.md is larger than 1 MiB") {
			t.Fatalf("Load = %v, want the SKILL.md size error", err)
		}
	}
	t.Run("directory", func(t *testing.T) {
		check(t, engine.NewDirSource(writeFiles(t, files)))
	})
	t.Run("git", func(t *testing.T) {
		repo := newGitRepo(t)
		repo.commit("main", files)
		check(t, newGit(t, engine.GitOptions{URL: repo.url}))
	})
	t.Run("a large data file is within the bundle cap", func(t *testing.T) {
		snap := load(t, map[string]string{
			globalSkillFile("x", "SKILL.md"): skillDoc("x", "d"),
			globalSkillFile("x", "big.bin"):  strings.Repeat("a", 2<<20),
		})
		if _, found := snap.SkillIn("", "x"); !found {
			t.Fatal("skill x not loaded")
		}
	})
}

func TestSkillRejectsUnsafePathNames(t *testing.T) {
	for _, name := range []string{".git/config", "a:b.md", "con.txt", "trail.", ".GIT/x"} {
		t.Run(name, func(t *testing.T) {
			_, err := engine.NewDirSource(writeFiles(t, map[string]string{
				globalSkillFile("x", "SKILL.md"): skillDoc("x", "d"),
				globalSkillFile("x", name):       "data",
			})).Load(t.Context())
			if err == nil {
				t.Fatal("Load succeeded, want the name rejected")
			}
		})
	}
}
