package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func localRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLocalRootNeedsAnArticlesDirectory(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  bool
	}{
		"articles":      {map[string]string{".goldie/articles/a.md": "x"}, true},
		"project only":  {map[string]string{".goldie/config.yaml": "project: github.com/a/b\n"}, false},
		"articles file": {map[string]string{".goldie/articles": "x"}, false},
		"nothing":       {map[string]string{"README.md": "x"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			root := localRepo(t, tc.files)
			sub := filepath.Join(root, "a", "b")
			if err := os.MkdirAll(sub, 0o750); err != nil {
				t.Fatal(err)
			}
			got, ok := LocalRoot(sub)
			if ok != tc.want || (ok && got != root) {
				t.Errorf("LocalRoot = %q, %v; want root %q, %v", got, ok, root, tc.want)
			}
		})
	}
}

func TestOpenConfigContentDir(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg     string
		want    string // the file to find through the handle, "" for no handle
		wantErr bool
	}{
		"no file":    {"", "", false},
		"no dir":     {"project: github.com/a/b\n", "", false},
		"null":       {"dir:\n", "", false},
		"nested":     {"dir: docs/content\n", "x.md", false},
		"dot":        {"dir: .\n", "docs/content/x.md", false},
		"absolute":   {"dir: /etc\n", "", true},
		"parent":     {"dir: ../x\n", "", true},
		"missing":    {"dir: nope\n", "", true},
		"integer":    {"dir: 5\n", "", true},
		"list":       {"dir: [a]\n", "", true},
		"empty":      {"dir: \"\"\n", "", true},
		"control":    {"dir: \"a\\u0007b\"\n", "", true},
		"bad yaml":   {"dir: [\n", "", true},
		"long value": {"dir: " + strings.Repeat("a", 5000) + "\n", "", true},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"docs/content/x.md": "x"}
			if tc.cfg != "" {
				files[".goldie/config.yaml"] = tc.cfg
			}
			root := localRepo(t, files)
			d, err := OpenConfigContentDir(root)
			if d != nil {
				defer func() { _ = d.Close() }()
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if err != nil {
				if d != nil {
					t.Error("a refused config returned a handle")
				}
				if !strings.Contains(err.Error(), ".goldie/config.yaml") || strings.Contains(err.Error(), root) {
					t.Errorf("error %q must name .goldie/config.yaml and no path", err)
				}
				return
			}
			if tc.want == "" {
				if d != nil {
					t.Error("got a handle, want nil")
				}
				return
			}
			if d == nil {
				t.Fatal("got no handle for a configured dir")
			}
			if _, err := d.Stat(tc.want); err != nil {
				t.Errorf("the handle does not reach %s: %v", tc.want, err)
			}
		})
	}
}

func TestOpenConfigContentDirRefusesASymlinkedConfig(t *testing.T) {
	root := localRepo(t, map[string]string{"shared/config.yaml": "dir: .\n"})
	if err := os.Mkdir(filepath.Join(root, ".goldie"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "shared", "config.yaml"), filepath.Join(root, ".goldie", "config.yaml")); err != nil {
		t.Skip(err)
	}
	d, err := OpenConfigContentDir(root)
	if d != nil {
		_ = d.Close()
	}
	if err == nil || d != nil {
		t.Errorf("a symlinked config.yaml was read: handle %v, err %v", d, err)
	}
}

func TestOpenConfigContentDirRefusesASymlinkOutOfTheRoot(t *testing.T) {
	outside := t.TempDir()
	root := localRepo(t, map[string]string{".goldie/config.yaml": "dir: link\n"})
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if _, err := OpenConfigContentDir(root); err == nil || strings.Contains(err.Error(), outside) {
		t.Errorf("err = %v, want a refusal without the target", err)
	}
}

// One policy for .goldie/config.yaml (#484): Detect, which reads `project:`,
// and OpenConfigContentDir, which reads `dir:`, give the same verdict on a
// symlinked file, even when the symlink points at a valid regular file.
func TestConfigYAMLSymlinkIsRefusedByDetectAndOpenConfigContentDir(t *testing.T) {
	root := localRepo(t, map[string]string{
		"shared/config.yaml": "project: github.com/acme/widgets\ndir: .\n",
	})
	if err := os.Mkdir(filepath.Join(root, ".goldie"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "shared", "config.yaml"), filepath.Join(root, ".goldie", "config.yaml")); err != nil {
		t.Skip(err)
	}
	got, err := Detect(root, "", "")
	if err == nil || got.ID != "" || !strings.Contains(err.Error(), ".goldie/config.yaml") || strings.Contains(err.Error(), root) {
		t.Errorf("Detect through a symlinked config.yaml = %+v, %v; want a refusal naming .goldie/config.yaml and no path", got, err)
	}
	d, err := OpenConfigContentDir(root)
	if d != nil {
		_ = d.Close()
	}
	if err == nil || d != nil {
		t.Errorf("OpenConfigContentDir through a symlinked config.yaml = %v, %v; want a refusal", d, err)
	}
}

// An oversize .goldie/config.yaml is refused by Detect and by
// OpenConfigContentDir alike, and the same file at the cap is read by both.
func TestConfigYAMLOversizeIsRefusedByDetectAndOpenConfigContentDir(t *testing.T) {
	head := "project: github.com/acme/widgets\ndir: .\n"
	root := localRepo(t, map[string]string{
		".goldie/config.yaml": head + "# " + strings.Repeat("x", maxProjectFile) + "\n",
	})
	if got, err := Detect(root, "", ""); err == nil || got.ID != "" || !strings.Contains(err.Error(), ".goldie/config.yaml") {
		t.Errorf("Detect on an oversize config.yaml = %+v, %v; want a refusal", got, err)
	}
	d, err := OpenConfigContentDir(root)
	if d != nil {
		_ = d.Close()
	}
	if err == nil || d != nil || !strings.Contains(err.Error(), ".goldie/config.yaml") {
		t.Errorf("OpenConfigContentDir on an oversize config.yaml = %v, %v; want a refusal", d, err)
	}

	exact := localRepo(t, map[string]string{
		".goldie/config.yaml": head + "#" + strings.Repeat("#", maxProjectFile-len(head)-1),
	})
	if got, err := Detect(exact, "", ""); err != nil || got.ID != "github.com/acme/widgets" {
		t.Errorf("Detect at the cap = %+v, %v; want the Project", got, err)
	}
	d, err = OpenConfigContentDir(exact)
	if err != nil || d == nil {
		t.Errorf("OpenConfigContentDir at the cap = %v, %v; want a handle", d, err)
	}
	if d != nil {
		_ = d.Close()
	}
}
