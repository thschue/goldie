package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The refusals of WriteConfig. They hold no path and no id; callers word them.
var (
	// ErrNotRepository: the directory holds no .git entry, so it is not a
	// repository root.
	ErrNotRepository = errors.New("not a repository root")
	// ErrSourceRepository: the repository root holds global/articles or
	// projects/, the layout of a goldie Source.
	ErrSourceRepository = errors.New("a Source repository")
	// ErrConfigExists: .goldie/config.yaml exists, as anything, even a symlink.
	ErrConfigExists = errors.New(".goldie/config.yaml exists")
	// ErrSymlink: .goldie is a symlink.
	ErrSymlink = errors.New(".goldie is a symlink")
)

// errGoldieNotDir refuses a .goldie that exists but is not a directory.
var errGoldieNotDir = errors.New(".goldie is not a directory")

// RepositoryRoot returns the repository root of dir: the nearest directory at
// or above it that holds a .git entry, a directory or the file of a worktree
// or submodule, as an absolute path. It is the root Detect reads
// .goldie/config.yaml up to. ok is false outside a repository.
func RepositoryRoot(dir string) (root string, ok bool) {
	root, _, ok = findGitConfig(dir)
	return root, ok
}

// IsSourceRepository reports whether root has the layout of a goldie Source:
// a global/articles or a projects/ directory at its root.
func IsSourceRepository(root string) bool {
	// The Source layout is owned by internal/engine/source.go (articlesDir,
	// loadFS); keep these markers in step with it.
	for _, marker := range []string{filepath.Join("global", "articles"), "projects"} {
		if fi, err := os.Stat(filepath.Join(root, marker)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// WriteConfig writes root/.goldie/config.yaml holding `project: <id>`, with id
// normalised as a typed Project is. root must be a repository root that is not
// a Source. It never overwrites or opens an existing config, never follows a
// symlinked .goldie or config.yaml, and writes nothing on a refusal: a .goldie
// it created is removed again. The file gets mode 0644 and the directory 0755,
// both less the umask. An error never holds the id or a path.
func WriteConfig(root, id string) error {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return ErrNotRepository
	}
	if IsSourceRepository(root) {
		return ErrSourceRepository
	}
	id, err := normaliseConfigured(strings.TrimSpace(id))
	if err != nil {
		return err
	}

	// os.Root resolves every name below root with openat, so no path above it
	// is touched, whatever a component is swapped for.
	r, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("cannot open the repository root: %w", cause(err))
	}
	defer func() { _ = r.Close() }()
	created, err := goldieDir(r)
	if err != nil {
		return err
	}
	if err := writeNew(r, id); err != nil {
		if created {
			_ = r.Remove(".goldie") // removes only the empty directory it made
		}
		return err
	}
	return nil
}

// goldieDir makes sure .goldie is a real directory under r, creating it when
// it is missing. Mkdir never follows a symlink, even a dangling one.
func goldieDir(r *os.Root) (created bool, err error) {
	fi, err := r.Lstat(".goldie")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := r.Mkdir(".goldie", 0o755); err != nil {
			return false, fmt.Errorf("cannot create .goldie: %w", cause(err))
		}
		return true, nil
	case err != nil:
		return false, fmt.Errorf("cannot read .goldie: %w", cause(err))
	case fi.Mode()&fs.ModeSymlink != 0:
		return false, ErrSymlink
	case !fi.IsDir():
		return false, errGoldieNotDir
	}
	return false, nil
}

// writeNew creates .goldie/config.yaml under r and writes the Project to it.
// It opens .goldie as a root of its own and checks that the directory it
// opened is the one Lstat sees, so a .goldie swapped for a symlink after
// goldieDir is refused. O_EXCL fails on any existing entry, a symlink (even a
// dangling one) or a FIFO included, without opening it.
func writeNew(r *os.Root, id string) (err error) {
	g, err := r.OpenRoot(".goldie")
	if err != nil {
		return fmt.Errorf("cannot open .goldie: %w", cause(err))
	}
	defer func() { _ = g.Close() }()
	opened, err := g.Stat(".")
	if err != nil {
		return fmt.Errorf("cannot read .goldie: %w", cause(err))
	}
	if fi, err := r.Lstat(".goldie"); err != nil || fi.Mode()&fs.ModeSymlink != 0 || !os.SameFile(fi, opened) {
		return ErrSymlink
	}
	f, err := g.OpenFile("config.yaml", os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return ErrConfigExists
	}
	if err != nil {
		return fmt.Errorf("cannot create .goldie/config.yaml: %w", cause(err))
	}
	_, werr := f.WriteString("project: " + id + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = g.Remove("config.yaml")
		return fmt.Errorf("cannot write .goldie/config.yaml: %w", cause(werr))
	}
	return nil
}

// cause drops the path an *fs.PathError or *os.LinkError carries and keeps the
// reason, such as "permission denied".
func cause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}
