package examples

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// #682: the built-in examples are examples/demo, a neutral demo platform:
// four articles, one skill with a script, one agent and one loop, laid out
// like a content directory. goldie's own agents, loops and article stay in
// examples/knowledge and are not embedded.

// entryNames lists the entries of dir in fsys.
func entryNames(t *testing.T, fsys fs.FS, dir string) []string {
	t.Helper()
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		t.Fatalf("the built-in examples have no %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestBuiltInExamplesAreADemoPlatformLaidOutLikeAContentDirectory(t *testing.T) {
	fsys := Knowledge()

	articles := entryNames(t, fsys, "global/articles")
	if len(articles) != 4 {
		t.Errorf("global/articles holds %v, want four articles", articles)
	}
	var keywords strings.Builder
	for _, a := range articles {
		b, err := fs.ReadFile(fsys, "global/articles/"+a)
		if err != nil {
			t.Fatal(err)
		}
		front, _, _ := strings.Cut(strings.TrimPrefix(string(b), "---\n"), "\n---\n")
		for _, l := range strings.Split(front, "\n") {
			if strings.HasPrefix(l, "keywords:") {
				keywords.WriteString(strings.ToLower(l) + "\n")
			}
		}
	}
	for _, want := range []string{"database", "logging", "retries", "feature flags"} {
		if !strings.Contains(keywords.String(), want) {
			t.Errorf("no article has the keyword %q:\n%s", want, keywords.String())
		}
	}

	skills := entryNames(t, fsys, "global/skills")
	if len(skills) != 1 {
		t.Fatalf("global/skills holds %v, want one skill", skills)
	}
	if _, err := fs.Stat(fsys, "global/skills/"+skills[0]+"/SKILL.md"); err != nil {
		t.Errorf("the skill has no SKILL.md: %v", err)
	}
	if scripts := entryNames(t, fsys, "global/skills/"+skills[0]+"/scripts"); len(scripts) == 0 {
		t.Error("the skill has an empty scripts directory, want a script")
	}
	if got := entryNames(t, fsys, "global/agents"); len(got) != 1 {
		t.Errorf("global/agents holds %v, want one agent", got)
	}
	if got := entryNames(t, fsys, "global/loops"); len(got) != 1 {
		t.Errorf("global/loops holds %v, want one loop", got)
	}
}

func TestBuiltInExamplesHoldNothingOfGoldiesOwnProcess(t *testing.T) {
	fsys := Knowledge()
	for _, dir := range []string{"agents", "loops"} {
		own, err := os.ReadDir("knowledge/global/" + dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range own {
			if _, err := fs.Stat(fsys, "global/"+dir+"/"+e.Name()); err == nil {
				t.Errorf("the built-in examples embed goldie's own global/%s/%s", dir, e.Name())
			}
		}
	}
	if _, err := fs.Stat(fsys, "global/articles/agent-loops.md"); err == nil {
		t.Error("the built-in examples embed goldie's own article agent-loops")
	}
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := fs.ReadFile(fsys, path)
		if rerr != nil {
			return rerr
		}
		for _, bad := range []string{"docs/agents", "./run ", "./run`", "thschue/goldie"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s mentions %q, which only goldie's own repository has", path, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Criterion 5: each README says which set is built in and which is goldie's own.
func TestExamplesDocumentWhichSetIsBuiltInAndWhichIsGoldiesOwn(t *testing.T) {
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return strings.ToLower(string(b))
	}
	demo := read("demo/README.md")
	if !strings.Contains(demo, "built in") && !strings.Contains(demo, "built-in") {
		t.Error("examples/demo/README.md does not say this set is built in")
	}
	if !strings.Contains(demo, "examples/knowledge") {
		t.Error("examples/demo/README.md does not name examples/knowledge as goldie's own set")
	}
	own := read("knowledge/README.md")
	if !strings.Contains(own, "examples/demo") {
		t.Error("examples/knowledge/README.md does not name examples/demo as the built-in set")
	}
	if !strings.Contains(own, "not embedded") && !strings.Contains(own, "not built in") && !strings.Contains(own, "not built-in") {
		t.Error("examples/knowledge/README.md does not say it is not built in")
	}
	if !strings.Contains(read("embed.go"), "examples/demo") {
		t.Error("the examples/embed.go doc comment does not name examples/demo")
	}
}
