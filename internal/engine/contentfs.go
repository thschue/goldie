package engine

import (
	"errors"
	"io/fs"
	"os"
)

// errNotRegular is what regularFS returns for an open that found something
// other than a regular file or a directory, such as a FIFO or a device.
var errNotRegular = errors.New("is not a regular file")

// contentFS returns the fs.FS Load reads root through. Like root.FS() it is
// confined to the root, but its Open does not block and refuses anything but a
// regular file or a directory. The walk and readProjectFile check a file's
// type before they open it; whoever can write the content directory could swap
// in a FIFO after that check, and a blocking open of a FIFO with no writer
// would hold the poll (#367). So the open is non-blocking (openFlags) and the
// open handle's own type decides. A symlink swapped in is followed inside the
// root, as os.Root does, and what it reaches is held to the same check.
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

func (c regularFS) ReadDir(name string) ([]fs.DirEntry, error) { return fs.ReadDir(c.meta, name) }
func (c regularFS) Stat(name string) (fs.FileInfo, error)      { return fs.Stat(c.meta, name) }
func (c regularFS) Lstat(name string) (fs.FileInfo, error)     { return fs.Lstat(c.meta, name) }
func (c regularFS) ReadLink(name string) (string, error)       { return fs.ReadLink(c.meta, name) }
