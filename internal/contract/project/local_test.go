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

func TestConfigContentDir(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg     string
		want    string // relative to the root, "" for none
		wantErr bool
	}{
		"no file":    {"", "", false},
		"no dir":     {"project: github.com/a/b\n", "", false},
		"null":       {"dir:\n", "", false},
		"nested":     {"dir: docs/content\n", "docs/content", false},
		"dot":        {"dir: .\n", ".", false},
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
			got, err := ConfigContentDir(root)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if err != nil {
				if !strings.Contains(err.Error(), ".goldie/config.yaml") || strings.Contains(err.Error(), root) {
					t.Errorf("error %q must name .goldie/config.yaml and no path", err)
				}
				return
			}
			want := ""
			if tc.want != "" {
				want = filepath.Join(root, tc.want)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestConfigContentDirRefusesASymlinkOutOfTheRoot(t *testing.T) {
	outside := t.TempDir()
	root := localRepo(t, map[string]string{".goldie/config.yaml": "dir: link\n"})
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if _, err := ConfigContentDir(root); err == nil || strings.Contains(err.Error(), outside) {
		t.Errorf("err = %v, want a refusal without the target", err)
	}
}
