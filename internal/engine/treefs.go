package engine

import (
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

var (
	errBadEntryName   = errors.New("tree entry name is not a valid file name")
	errDuplicateEntry = errors.New("tree has two entries with one name")
)

// Load limits for one treeFS. A tree that references the same subtree many
// times expands to far more paths than it has objects, and one blob can back
// many files, so a hostile tree could make a load run and allocate without
// bound. These cap the work and the content one load may read.
const (
	maxTreeSteps   = 1 << 20   // entries listed plus path elements stepped through
	maxTreeBytes   = 256 << 20 // file content read
	maxTreePathLen = 4096      // bytes in one path
)

// treeFS is a read-only fs.FS over one git tree, so the shared loader reads a
// commit without a working tree. It never follows a symlink: opening one or a
// path through one fails, and only Lstat reports it. A submodule is an
// irregular entry. A tree entry whose name could traverse makes ReadDir fail.
// Its budget is shared by every call, so build one treeFS per load.
type treeFS struct {
	root *object.Tree
	b    *treeBudget
}

func newTreeFS(root *object.Tree) treeFS {
	return treeFS{root: root, b: &treeBudget{
		steps: maxTreeSteps, bytes: maxTreeBytes,
		trees: map[plumbing.Hash]*object.Tree{}, names: map[plumbing.Hash]map[string]int{},
	}}
}

// loadTree loads one git tree with a fresh treeFS, so its load limits apply
// per load: a Source calls it on every load and never keeps a treeFS between
// loads, as DirSource.Load opens its os.Root per call.
func loadTree(root *object.Tree) (*Snapshot, error) { return loadFS(newTreeFS(root)) }

// treeBudget is what a treeFS may still spend, the subtrees it decoded and
// their name indexes.
type treeBudget struct {
	mu    sync.Mutex
	steps int
	bytes int
	trees map[plumbing.Hash]*object.Tree
	names map[plumbing.Hash]map[string]int
}

// index maps each entry name of t to its position, once per tree hash. A
// listing charges the entries to the step budget every time; a lookup charges
// them only when it builds the index. Two entries with one name would make a
// lookup and a listing disagree, so they fail.
func (b *treeBudget) index(t *object.Tree, listing bool) (map[string]int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m, ok := b.names[t.Hash]
	if ok && !listing {
		return m, nil
	}
	if err := b.spendLocked(len(t.Entries)); err != nil {
		return nil, err
	}
	if ok {
		return m, nil
	}
	m = make(map[string]int, len(t.Entries))
	for i, e := range t.Entries {
		if _, dup := m[e.Name]; dup {
			return nil, errDuplicateEntry
		}
		m[e.Name] = i
	}
	b.names[t.Hash] = m
	return m, nil
}

func (b *treeBudget) spend(steps int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spendLocked(steps)
}

func (b *treeBudget) spendLocked(steps int) error {
	if steps > b.steps {
		b.steps = 0
		return errLoadLimit
	}
	b.steps -= steps
	return nil
}

func (b *treeBudget) spendBytes(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.bytes {
		b.bytes = 0
		return errLoadLimit
	}
	b.bytes -= n
	return nil
}

// subtree decodes the directory entry e of parent once per tree hash. The
// caller has indexed parent, so e.Name names e alone.
func (b *treeBudget) subtree(parent *object.Tree, e object.TreeEntry) (*object.Tree, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.trees[e.Hash]; ok {
		return t, nil
	}
	t, err := parent.Tree(e.Name)
	if err != nil {
		return nil, err
	}
	b.trees[e.Hash] = t
	return t, nil
}

// budgetReader charges what it reads to the treeFS byte budget.
type budgetReader struct {
	io.ReadCloser
	b *treeBudget
}

func (r budgetReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		if berr := r.b.spendBytes(n); berr != nil {
			return 0, berr
		}
	}
	return n, err
}

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
	if !fs.ValidPath(name) || len(name) > maxTreePathLen {
		return fail(fs.ErrInvalid)
	}
	n := node{dir: f.root}
	if name == "." {
		return n, nil
	}
	for part := range strings.SplitSeq(name, "/") {
		if err := f.b.spend(1); err != nil {
			return fail(err)
		}
		if n.dir == nil {
			if n.entry.Mode == filemode.Symlink {
				// Not followed, and not reported missing either, so a
				// symlinked directory fails the load instead of loading nothing.
				return fail(fs.ErrInvalid)
			}
			return fail(fs.ErrNotExist)
		}
		names, err := f.b.index(n.dir, false)
		if err != nil {
			return fail(err)
		}
		i, ok := names[part]
		if !ok {
			return fail(fs.ErrNotExist)
		}
		n = node{entry: n.dir.Entries[i], parent: n.dir}
		if n.entry.Mode == filemode.Dir {
			sub, err := f.b.subtree(n.parent, n.entry)
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
		return &treeFile{info: n.info(), ReadCloser: budgetReader{r, f.b}}, nil
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
	target, err := io.ReadAll(io.LimitReader(budgetReader{r, f.b}, 4096))
	return string(target), err
}

func (f treeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	n, err := f.lookup("readdir", name)
	if err == nil && n.dir == nil {
		err = &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	if err == nil {
		if _, ierr := f.b.index(n.dir, true); ierr != nil {
			err = &fs.PathError{Op: "readdir", Path: name, Err: ierr}
		}
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
