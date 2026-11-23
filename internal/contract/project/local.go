package project

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// maxDirValue bounds the length of config.yaml's dir, which is untrusted.
const maxDirValue = 4096

// LocalRoot returns the repository root of dir when the repository has a local
// Scope: .goldie is a directory and .goldie/articles is a directory in it, and
// neither is a symlink. A .goldie with only config.yaml (what goldie init
// writes) has none. Outside a repository only dir itself counts, as in
// Detect. It reads no article and no file: it finds paths.
func LocalRoot(dir string) (root string, ok bool) {
	root, ok = RepositoryRoot(dir)
	if !ok {
		var err error
		if root, err = filepath.Abs(dir); err != nil {
			return "", false
		}
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return "", false
	}
	defer func() { _ = r.Close() }()
	for _, name := range []string{".goldie", ".goldie/articles"} {
		if fi, err := r.Lstat(name); err != nil || !fi.IsDir() {
			return "", false
		}
	}
	return root, true
}

// GoldieDir is the absolute path of the .goldie directory of a repository root.
func GoldieDir(root string) string { return filepath.Join(root, ".goldie") }

// OpenConfigContentDir returns the content directory that root's
// .goldie/config.yaml names with `dir:`, opened through the repository root, so
// the caller reads through the handle and a path swapped for a symlink after
// the check cannot redirect it. It returns a nil handle when config.yaml names
// none (no file, no `dir:` key or a null one). The caller closes it. Only the
// dir key is read. The value is untrusted: it must be a non-empty string
// without control characters, a relative path that stays inside root, and name
// a directory, which is opened through os.Root so a symlink cannot lead out of
// root. Every error names .goldie/config.yaml and holds no path and no part of
// the value.
func OpenConfigContentDir(root string) (*os.Root, error) {
	d, _, err := openConfigContentDir(root)
	return d, err
}

func openConfigContentDir(root string) (*os.Root, string, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", errors.New("cannot open the repository root")
	}
	defer func() { _ = r.Close() }()
	b, err := readConfig(r)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", errors.New("cannot read .goldie/config.yaml")
	}
	var cfg struct {
		Dir yaml.Node `yaml:"dir"`
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, "", errors.New(".goldie/config.yaml is not valid YAML")
	}
	switch {
	case cfg.Dir.Kind == 0, cfg.Dir.Tag == "!!null":
		return nil, "", nil
	case cfg.Dir.Kind != yaml.ScalarNode || cfg.Dir.Tag != "!!str":
		return nil, "", errors.New(".goldie/config.yaml: dir must be a string")
	}
	rel := cfg.Dir.Value
	bad := errors.New(".goldie/config.yaml: dir must name a directory inside the repository, as a relative path")
	if rel == "" || len(rel) > maxDirValue || strings.ContainsFunc(rel, unicode.IsControl) || !filepath.IsLocal(rel) {
		return nil, "", bad
	}
	rel = filepath.Clean(rel)
	d, err := r.OpenRoot(rel) // fails on a file, on a missing path and on a symlink out of root
	if err != nil {
		return nil, "", bad
	}
	return d, rel, nil
}

// readConfigIn reads dir's .goldie/config.yaml through an os.Root opened at dir.
// A .goldie that is a symlink holds no config for detection: nothing is read
// through it, and the walk goes on, as it did when the link dangled.
func readConfigIn(dir string) ([]byte, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if fi, err := r.Lstat(".goldie"); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fs.ErrNotExist
	}
	return readConfig(r)
}

// readConfig reads .goldie/config.yaml through r, the one reader of that file:
// a symlink is refused, and it must be a regular file of at most maxProjectFile
// bytes, opened without blocking.
func readConfig(r *os.Root) ([]byte, error) {
	const name = ".goldie/config.yaml"
	if fi, err := r.Lstat(name); err != nil || fi.Mode()&fs.ModeSymlink != 0 {
		if err == nil {
			err = errNotRegular
		}
		return nil, err
	}
	return readRegular(r, name)
}

// readRegular opens name through r without blocking and reads it with
// readOpened.
func readRegular(r *os.Root, name string) ([]byte, error) {
	f, err := r.OpenFile(name, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readOpened(f)
}

// readOpened checks the type of the file f opened and reads at most
// maxProjectFile bytes of it.
func readOpened(f *os.File) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular
	}
	b, err := io.ReadAll(io.LimitReader(f, maxProjectFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxProjectFile {
		return nil, errFileTooLarge
	}
	return b, nil
}
