package engine

import (
	"errors"
	"io"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

var errBadEntryName = errors.New("tree entry name is not a valid file name")

// treeFS is a read-only fs.FS over one git tree, so the shared loader reads a
// commit without a working tree. It never follows a symlink: opening one or a
// path through one fails, and only Lstat reports it. A submodule is an
// irregular entry. A tree entry whose name could traverse makes ReadDir fail.
type treeFS struct{ root *object.Tree }

func newTreeFS(root *object.Tree) treeFS { return treeFS{root: root} }

var (
	_ fs.ReadDirFS  = treeFS{}
	_ fs.ReadLinkFS = treeFS{}
)

// node is a resolved path: the root (parent nil) or an entry of parent. dir is
// set for directories.
type node struct {
	entry  object.TreeEntry
	parent *object.Tree
	dir    *object.Tree
}

func (n node) mode() fs.FileMode {
	switch {
	case n.parent == nil || n.entry.Mode == filemode.Dir:
		return fs.ModeDir | 0o555
	case n.entry.Mode == filemode.Regular || n.entry.Mode == filemode.Deprecated:
		return 0o444
	case n.entry.Mode == filemode.Executable:
		return 0o555
	case n.entry.Mode == filemode.Symlink:
		return fs.ModeSymlink | 0o777
	}
	return fs.ModeIrregular
}

func (n node) info() fs.FileInfo {
	fi := fileInfo{name: n.entry.Name, mode: n.mode()}
	if n.parent == nil {
		fi.name = "."
	} else if fi.mode.Type()&^fs.ModeSymlink == 0 {
		if f, err := n.parent.TreeEntryFile(&n.entry); err == nil {
			fi.size = f.Size
		}
	}
	return fi
}

func (n node) reader() (io.ReadCloser, error) {
	f, err := n.parent.TreeEntryFile(&n.entry)
	if err != nil {
		return nil, err
	}
	return f.Reader()
}

func (f treeFS) lookup(op, name string) (node, error) {
	fail := func(err error) (node, error) { return node{}, &fs.PathError{Op: op, Path: name, Err: err} }
	if !fs.ValidPath(name) {
		return fail(fs.ErrInvalid)
	}
	n := node{dir: f.root}
	if name == "." {
		return n, nil
	}
	for part := range strings.SplitSeq(name, "/") {
		if n.dir == nil {
			if n.entry.Mode == filemode.Symlink {
				// Not followed, and not reported missing either, so a
				// symlinked directory fails the load instead of loading nothing.
				return fail(fs.ErrInvalid)
			}
			return fail(fs.ErrNotExist)
		}
		i := slices.IndexFunc(n.dir.Entries, func(e object.TreeEntry) bool { return e.Name == part })
		if i < 0 {
			return fail(fs.ErrNotExist)
		}
		n = node{entry: n.dir.Entries[i], parent: n.dir}
		if n.entry.Mode == filemode.Dir {
			sub, err := n.parent.Tree(part)
			if err != nil {
				return fail(err)
			}
			n.dir = sub
		}
	}
	return n, nil
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
		r, err := n.reader()
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		return &treeFile{info: n.info(), ReadCloser: r}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
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
	if err == nil && n.mode().Type() != fs.ModeSymlink {
		err = &fs.PathError{Op: "readlink", Path: name, Err: fs.ErrInvalid}
	}
	if err != nil {
		return "", err
	}
	r, err := n.reader()
	if err != nil {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: err}
	}
	defer func() { _ = r.Close() }()
	target, err := io.ReadAll(io.LimitReader(r, 4096))
	return string(target), err
}

func (f treeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	n, err := f.lookup("readdir", name)
	if err == nil && n.dir == nil {
		err = &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(n.dir.Entries))
	for _, e := range n.dir.Entries {
		if !validEntryName(e.Name) {
			return nil, &fs.PathError{Op: "readdir", Path: name, Err: errBadEntryName}
		}
		out = append(out, fs.FileInfoToDirEntry(node{entry: e, parent: n.dir}.info()))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// validEntryName rejects names a hostile tree could use to traverse or to make
// fs.WalkDir revisit a directory.
func validEntryName(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "/\x00")
}

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
	info fs.FileInfo
	io.ReadCloser
}

func (f *treeFile) Stat() (fs.FileInfo, error) { return f.info, nil }

type treeDir struct {
	info    fs.FileInfo
	entries []fs.DirEntry
}

func (d *treeDir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *treeDir) Close() error               { return nil }
func (d *treeDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.Name(), Err: fs.ErrInvalid}
}

func (d *treeDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if n > 0 && len(d.entries) == 0 {
		return nil, io.EOF
	}
	if n <= 0 || n > len(d.entries) {
		n = len(d.entries)
	}
	out := d.entries[:n]
	d.entries = d.entries[n:]
	return out, nil
}
