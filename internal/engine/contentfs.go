package engine

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// errNotRegular is what regularFS returns for an open that found something
// other than a regular file or a directory, such as a FIFO or a device.
var errNotRegular = errors.New("is not a regular file")

// errNotDir is what regularFS.ReadDir returns when what it opened is not a
// directory, such as a FIFO swapped in for one.
var errNotDir = errors.New("is not a directory")

// contentFS returns the fs.FS Load reads root through. Like root.FS() it is
// confined to the root, but neither its Open nor its ReadDir blocks: Open
// refuses anything but a regular file or a directory, and ReadDir anything but
// a directory. The walks and readProjectFile check an entry's type before they
// open or list it; whoever can write the content directory could swap in a
// FIFO after that check, and a blocking open of a FIFO with no writer would
// hold the poll (#367). So every open, a directory's included, is non-blocking
// (openFlags), and the open handle's own type decides. Stat, Lstat and
// ReadLink open nothing and go through root.FS(). A symlink swapped in is
// followed inside the root, as os.Root does, and what it reaches is held to
// the same check.
func (DirSource) contentFS(root *os.Root) fs.FS {
	return regularFS{root: root, meta: root.FS()}
}

// regularFS reads metadata through meta, root.FS(), and opens through root.
// It does not implement fs.ReadFileFS, so every read goes through Open.
type regularFS struct {
	root *os.Root
	meta fs.FS
}

var (
	_ fs.ReadDirFS  = regularFS{}
	_ fs.StatFS     = regularFS{}
	_ fs.ReadLinkFS = regularFS{}
)

func (c regularFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	f, err := c.root.OpenFile(name, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err == nil && !fi.Mode().IsRegular() && !fi.IsDir() {
		err = &fs.PathError{Op: "open", Path: name, Err: errNotRegular}
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// ReadDir lists a directory through Open, never root.FS(), whose ReadDir opens
// with a blocking O_RDONLY: a FIFO swapped in for a directory after its
// parent's listing would hold the poll there. What Open returns must be a
// directory. The entries are sorted by name, as fs.ReadDir does.
func (c regularFS) ReadDir(name string) ([]fs.DirEntry, error) {
	f, err := c.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	d, ok := f.(fs.ReadDirFile)
	if fi, err := f.Stat(); err != nil || !ok || !fi.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: errNotDir}
	}
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

func (c regularFS) Stat(name string) (fs.FileInfo, error)  { return fs.Stat(c.meta, name) }
func (c regularFS) Lstat(name string) (fs.FileInfo, error) { return fs.Lstat(c.meta, name) }
func (c regularFS) ReadLink(name string) (string, error)   { return fs.ReadLink(c.meta, name) }
