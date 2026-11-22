package engine

import (
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// maxLinkBytes caps how much of a symlink target ReadLink reads.
const maxLinkBytes = 4096

var errBadEntryName = errors.New("tree entry name is not a valid file name")

// treeFS is a read-only fs.FS over one git tree, so the shared loader can read
// a commit without a working tree. It never follows a symlink: Open and Stat
// of one fail, Lstat reports it, and a path through one does not exist. A
// submodule is an irregular entry. A tree entry whose name could traverse
// ("", ".", "..", or one with '/' or NUL) makes ReadDir fail.
type treeFS struct{ root *object.Tree }

func newTreeFS(root *object.Tree) treeFS { return treeFS{root: root} }

var (
	_ fs.ReadDirFS  = treeFS{}
	_ fs.StatFS     = treeFS{}
	_ fs.ReadLinkFS = treeFS{}
)

// node is a resolved path: the root, or an entry of its parent tree. dir is
// set for directories.
type node struct {
	name   string
	entry  object.TreeEntry
	parent *object.Tree
	dir    *object.Tree
}

func (n node) mode() fs.FileMode {
	if n.parent == nil {
		return fs.ModeDir | 0o555
	}
	switch n.entry.Mode {
	case filemode.Dir:
		return fs.ModeDir | 0o555
	case filemode.Regular, filemode.Deprecated:
		return 0o444
	case filemode.Executable:
		return 0o555
	case filemode.Symlink:
		return fs.ModeSymlink | 0o777
	default:
		return fs.ModeIrregular
	}
}

func (n node) info() fileInfo {
	fi := fileInfo{name: n.name, mode: n.mode()}
	if fi.mode.Type() == 0 || fi.mode.Type() == fs.ModeSymlink {
		if f, err := n.parent.TreeEntryFile(&n.entry); err == nil {
			fi.size = f.Size
		}
	}
	return fi
}

func (f treeFS) lookup(op, name string) (node, error) {
	if !fs.ValidPath(name) {
		return node{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return node{name: ".", dir: f.root}, nil
	}
	cur := f.root
	parts := strings.Split(name, "/")
	for i, part := range parts {
		e, ok := findEntry(cur, part)
		if ok && i < len(parts)-1 && e.Mode == filemode.Symlink {
			// Not followed, and not reported as missing either, so a
			// symlinked directory fails the load instead of loading nothing.
			return node{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
		}
		if !ok || (i < len(parts)-1 && e.Mode != filemode.Dir) {
			return node{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
		}
		var sub *object.Tree
		if e.Mode == filemode.Dir {
			var err error
			if sub, err = cur.Tree(part); err != nil {
				return node{}, &fs.PathError{Op: op, Path: name, Err: err}
			}
		}
		if i == len(parts)-1 {
			return node{name: part, entry: e, parent: cur, dir: sub}, nil
		}
		cur = sub
	}
	return node{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
}

func findEntry(t *object.Tree, name string) (object.TreeEntry, bool) {
	for _, e := range t.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return object.TreeEntry{}, false
}

func (f treeFS) Open(name string) (fs.File, error) {
	n, err := f.lookup("open", name)
	if err != nil {
		return nil, err
	}
	switch n.mode().Type() {
	case fs.ModeDir:
		entries, err := f.ReadDir(name)
		if err != nil {
			return nil, err
		}
		return &treeDir{info: n.info(), entries: entries}, nil
	case 0:
		file, err := n.parent.TreeEntryFile(&n.entry)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		r, err := file.Reader()
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		return &treeFile{info: n.info(), r: r}, nil
	default:
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
}

// Stat does not follow a symlink: it fails on one.
func (f treeFS) Stat(name string) (fs.FileInfo, error) {
	n, err := f.lookup("stat", name)
	if err != nil {
		return nil, err
	}
	if n.mode().Type() == fs.ModeSymlink {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	return n.info(), nil
}

func (f treeFS) Lstat(name string) (fs.FileInfo, error) {
	n, err := f.lookup("lstat", name)
	if err != nil {
		return nil, err
	}
	return n.info(), nil
}

func (f treeFS) ReadLink(name string) (string, error) {
	n, err := f.lookup("readlink", name)
	if err != nil {
		return "", err
	}
	if n.mode().Type() != fs.ModeSymlink {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: fs.ErrInvalid}
	}
	file, err := n.parent.TreeEntryFile(&n.entry)
	if err != nil {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: err}
	}
	r, err := file.Reader()
	if err != nil {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: err}
	}
	defer func() { _ = r.Close() }()
	target, err := io.ReadAll(io.LimitReader(r, maxLinkBytes))
	if err != nil {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: err}
	}
	return string(target), nil
}

func (f treeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	n, err := f.lookup("readdir", name)
	if err != nil {
		return nil, err
	}
	if n.dir == nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	out := make([]fs.DirEntry, 0, len(n.dir.Entries))
	for _, e := range n.dir.Entries {
		if !validEntryName(e.Name) {
			return nil, &fs.PathError{Op: "readdir", Path: name, Err: errBadEntryName}
		}
		out = append(out, dirEntry{node{name: e.Name, entry: e, parent: n.dir}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// validEntryName rejects names a hostile tree could use to traverse or to make
// fs.WalkDir revisit a directory.
func validEntryName(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "/\x00")
}

type dirEntry struct{ n node }

func (d dirEntry) Name() string               { return d.n.name }
func (d dirEntry) IsDir() bool                { return d.n.mode().IsDir() }
func (d dirEntry) Type() fs.FileMode          { return d.n.mode().Type() }
func (d dirEntry) Info() (fs.FileInfo, error) { return d.n.info(), nil }

type fileInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (fi fileInfo) Name() string       { return fi.name }
func (fi fileInfo) Size() int64        { return fi.size }
func (fi fileInfo) Mode() fs.FileMode  { return fi.mode }
func (fi fileInfo) ModTime() time.Time { return time.Time{} }
func (fi fileInfo) IsDir() bool        { return fi.mode.IsDir() }
func (fi fileInfo) Sys() any           { return nil }

type treeFile struct {
	info fileInfo
	r    io.ReadCloser
}

func (f *treeFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *treeFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *treeFile) Close() error               { return f.r.Close() }

type treeDir struct {
	info    fileInfo
	entries []fs.DirEntry
	off     int
}

func (d *treeDir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *treeDir) Close() error               { return nil }
func (d *treeDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.name, Err: fs.ErrInvalid}
}

func (d *treeDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.off:]
	if n <= 0 {
		d.off = len(d.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	n = min(n, len(rest))
	d.off += n
	return rest[:n], nil
}
