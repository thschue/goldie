package cli

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/thschue/goldie/internal/contract"
)

// stampName is the digest stamp, in .claude beside the namespace: the digest of
// the set last pulled, one line.
const stampName = "goldie-pull.digest"

// namespaceDirs are the directories under .claude that hold namespace entries.
var namespaceDirs = []string{"skills", "agents"}

var digestRE = regexp.MustCompile(`^[0-9A-Za-z._:-]{1,128}$`)

var errLinked = errors.New("is a symlink or not a directory; goldie pull never writes through it")

// plainDir reports whether name under root is absent (exists false) or a real
// directory. A symlink, a file or anything else is an error: the namespace is
// never written through a link.
func plainDir(root *os.Root, name string) (exists bool, err error) {
	fi, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("cannot read %s", name)
	case !fi.IsDir():
		return true, fmt.Errorf("%s %w", name, errLinked)
	}
	return true, nil
}

// readStamp returns the digest the last pull stamped, or "" when there is no
// usable stamp (never pulled, unreadable, or not a digest).
func readStamp(root *os.Root) string {
	if ok, err := plainDir(root, ".claude"); !ok || err != nil {
		return ""
	}
	name := ".claude/" + stampName
	if fi, err := root.Lstat(name); err != nil || !fi.Mode().IsRegular() || fi.Size() > 256 {
		return ""
	}
	f, err := root.Open(name)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 257))
	if err != nil {
		return ""
	}
	if d := strings.TrimSpace(string(b)); digestRE.MatchString(d) {
		return d
	}
	return ""
}

// namespaceEntries lists the entries of the Goldie namespace that exist under
// root, as .claude/<dir>/goldie-<name> paths. Whatever else sits in those
// directories is not listed.
func namespaceEntries(root *os.Root) ([]string, error) {
	var out []string
	for _, d := range namespaceDirs {
		dir := ".claude/" + d
		exists, err := plainDir(root, dir)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		entries, err := fs.ReadDir(root.FS(), dir)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s", dir)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), goldiePrefix) {
				out = append(out, dir+"/"+e.Name())
			}
		}
	}
	return out, nil
}

// checkedNamespaceEntries refuses a symlinked or non-directory .claude,
// .claude/skills or .claude/agents, then lists the namespace entries. The real
// pull and the dry run both go through it, so they refuse identically.
func checkedNamespaceEntries(root *os.Root) ([]string, error) {
	for _, d := range []string{".claude", ".claude/skills", ".claude/agents"} {
		if _, err := plainDir(root, d); err != nil {
			return nil, err
		}
	}
	return namespaceEntries(root)
}

// topEntry is the namespace entry a file belongs to: its first three elements.
func topEntry(p string) string {
	return strings.Join(strings.SplitN(p, "/", 4)[:3], "/")
}

// applyPull replaces the Goldie namespace under root by files, which
// extractBundle checked, then stamps digest. It stages the new tree inside
// .claude, moves the old entries aside, moves the new ones in, and puts
// everything back if a step fails, so the project is not left half pulled.
// Nothing outside the namespace and the stamp is touched, and no path is
// written through a symlink: .claude, .claude/skills and .claude/agents must be
// real directories, and old entries are moved or removed as themselves.
func applyPull(root *os.Root, files []pulledFile, digest string) (err error) {
	old, err := checkedNamespaceEntries(root)
	if err != nil {
		return err
	}
	for _, d := range []string{".claude/skills", ".claude/agents"} {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("cannot create %s", d)
		}
	}
	var r [8]byte
	_, _ = rand.Read(r[:])
	tmp := ".claude/.goldie-pull-" + hex.EncodeToString(r[:])
	if err := root.Mkdir(tmp, 0o755); err != nil {
		return errors.New("cannot write in .claude")
	}
	defer func() { _ = root.RemoveAll(tmp) }()

	if err := stage(root, tmp+"/new", files); err != nil {
		return err
	}
	fresh := map[string]bool{}
	for _, f := range files {
		fresh[topEntry(f.path)] = true
	}
	var moved, placed [][2]string // from, to
	undo := func() {
		for _, m := range slices.Backward(placed) {
			_ = root.RemoveAll(m[1])
		}
		for _, m := range slices.Backward(moved) {
			_ = root.Rename(m[1], m[0])
		}
	}
	for i, o := range old {
		aside := fmt.Sprintf("%s/old/%d", tmp, i)
		if err := root.MkdirAll(path.Dir(aside), 0o755); err != nil {
			undo()
			return errors.New("cannot write in .claude")
		}
		if err := root.Rename(o, aside); err != nil {
			undo()
			return errors.New("cannot replace " + contract.CleanLine(o))
		}
		moved = append(moved, [2]string{o, aside})
	}
	for _, top := range slices.Sorted(maps.Keys(fresh)) {
		if err := root.Rename(tmp+"/new/"+strings.TrimPrefix(top, ".claude/"), top); err != nil {
			undo()
			return errors.New("cannot write " + top)
		}
		placed = append(placed, [2]string{"", top})
	}
	sub, err := root.OpenRoot(".claude")
	if err != nil {
		undo()
		return errors.New("cannot write in .claude")
	}
	defer func() { _ = sub.Close() }()
	if err := writeInstructionFile(sub, stampName, []byte(digest+"\n"), 0o644); err != nil {
		undo()
		return errors.New("cannot write .claude/" + stampName)
	}
	return nil
}

// stage writes files under base (relative to root), each at its path without
// the leading ".claude/", with the executable bit when it has one.
func stage(root *os.Root, base string, files []pulledFile) error {
	for _, f := range files {
		dst := base + "/" + strings.TrimPrefix(f.path, ".claude/")
		if err := root.MkdirAll(path.Dir(dst), 0o755); err != nil {
			return errors.New("cannot write in .claude")
		}
		mode := os.FileMode(0o644)
		if f.exec {
			mode = 0o755
		}
		w, err := root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return errors.New("cannot write " + f.path)
		}
		_, werr := w.Write(f.data)
		if werr == nil {
			werr = w.Chmod(mode)
		}
		if cerr := w.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return errors.New("cannot write " + f.path)
		}
	}
	return nil
}
