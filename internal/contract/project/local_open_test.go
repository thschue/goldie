package project

import (
	"os"
	"path/filepath"
	"testing"
)

// The handle OpenConfigContentDir returns is the directory the check opened:
// swapping its path for a symlink afterwards does not redirect reads.
func TestOpenConfigContentDirSurvivesASwap(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(outside, "secret.md"), []byte("x"), 0o644))
	must(os.MkdirAll(filepath.Join(root, ".goldie"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "kb"), 0o755))
	must(os.WriteFile(filepath.Join(root, "kb", "ok.md"), []byte("ok"), 0o644))
	must(os.WriteFile(filepath.Join(root, ".goldie", "config.yaml"), []byte("dir: kb\n"), 0o644))

	d, err := OpenConfigContentDir(root)
	must(err)
	if d == nil {
		t.Fatal("no handle for a configured dir")
	}
	defer func() { _ = d.Close() }()

	must(os.Rename(filepath.Join(root, "kb"), filepath.Join(root, "kb-old")))
	must(os.Symlink(outside, filepath.Join(root, "kb")))

	if _, err := d.Stat("ok.md"); err != nil {
		t.Errorf("handle lost the validated directory: %v", err)
	}
	if _, err := d.Stat("secret.md"); err == nil {
		t.Error("handle follows the swapped symlink out of the repository")
	}
}

func TestOpenConfigContentDirWithoutDirIsNil(t *testing.T) {
	d, err := OpenConfigContentDir(t.TempDir())
	if err != nil || d != nil {
		t.Fatalf("got %v, %v; want nil, nil", d, err)
	}
}
