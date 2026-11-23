package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #517: every Kind the engine serves is checked by Claims, in every
// Scope that Claims govern. A Kind added later must be added to this table;
// the case for it fails until the Claims check covers it.
var claimKinds = []struct {
	kind, global, project, globalDir, projectDir string
}{
	{"article", "global/articles/rules.md", "projects/github.com/other/x/articles/rules.md",
		"global/articles/rules", "projects/github.com/other/x/articles/rules"},
	{"agent", "global/agents/bot.md", "projects/github.com/other/x/agents/bot.md",
		"global/agents/bot", "projects/github.com/other/x/agents/bot"},
	{"skill", "global/skills/deploy/SKILL.md", "projects/github.com/other/x/skills/deploy/SKILL.md",
		"global/skills/deploy", "projects/github.com/other/x/skills/deploy"},
}

func claimKindBody(kind string) string {
	if kind == "article" {
		return article("Rules")
	}
	name := map[string]string{"agent": "bot", "skill": "deploy"}[kind]
	return "---\nname: " + name + "\ndescription: d\n---\n# " + name + "\n"
}

func TestClaimsRejectAnUnclaimedItemOfEveryKind(t *testing.T) {
	for _, k := range claimKinds {
		t.Run(k.kind+"/global", func(t *testing.T) {
			// A Project-only Claim does not accept a global Item.
			dir := writeFiles(t, map[string]string{k.global: claimKindBody(k.kind)})
			_, err := engine.NewClaimedSource(engine.NewDirSource(dir),
				engine.Claim{Projects: []string{claimAcmeGlob}}).Load(t.Context())
			if err == nil {
				t.Fatalf("unclaimed global %s loaded", k.kind)
			}
			if !strings.Contains(err.Error(), k.globalDir) || strings.Contains(err.Error(), dir) {
				t.Errorf("error %q must name %s and no host path", err, k.globalDir)
			}
		})
		t.Run(k.kind+"/project", func(t *testing.T) {
			// A Claim for another Project does not accept this Project's Item.
			dir := writeFiles(t, map[string]string{k.project: claimKindBody(k.kind)})
			_, err := engine.NewClaimedSource(engine.NewDirSource(dir),
				engine.Claim{Global: true, Projects: []string{claimAcmeGlob}}).Load(t.Context())
			if err == nil {
				t.Fatalf("unclaimed project %s loaded", k.kind)
			}
			if !strings.Contains(err.Error(), k.projectDir) || strings.Contains(err.Error(), dir) {
				t.Errorf("error %q must name %s and no host path", err, k.projectDir)
			}
		})
		t.Run(k.kind+"/claimed", func(t *testing.T) {
			// The same Items load once claimed: the rejection is the Claim, not the Item.
			claimLoad(t, engine.Claim{Global: true, Projects: []string{"github.com/other/*"}},
				map[string]string{k.global: claimKindBody(k.kind), k.project: claimKindBody(k.kind)})
		})
	}
}

// A symlinked articles directory fails the load in every Scope, whether it
// points outside the root or inside it, and the error names no host path.
func TestSymlinkedArticlesDirectoryFailsTheLoadInEveryScope(t *testing.T) {
	for _, dir := range []string{
		"global/articles",
		"projects/github.com/acme/api/articles",
	} {
		for name, inRoot := range map[string]bool{"outside the root": false, "inside the root": true} {
			t.Run(dir+" "+name, func(t *testing.T) {
				root := t.TempDir()
				target := t.TempDir()
				if inRoot {
					target = filepath.Join(root, "elsewhere")
				}
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "o.md"), []byte(article("Linked")), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, dir)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, dir)); err != nil {
					t.Skip("symlinks unavailable")
				}
				snap, err := engine.NewDirSource(root).Load(t.Context())
				if err == nil {
					t.Fatalf("loaded %d articles through a symlinked %s", len(snap.Articles()), dir)
				}
				if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), target) {
					t.Errorf("error leaks a host path: %v", err)
				}
			})
		}
	}
}
