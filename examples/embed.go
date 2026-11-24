// Package examples embeds examples/demo, a neutral demo platform: the example
// content goldie server --dev serves from a directory with none of its own.
// The directory is the one source of truth: the binary embeds it as it is,
// except that the executable bit, which embed.FS drops, is restored from the
// executable list below. goldie's own agents and loops stay in
// examples/knowledge and are not embedded.
package examples

import (
	"embed"
	"io/fs"
	"path"
)

//go:embed all:demo
var demo embed.FS

// executable lists the files stored with mode 100755 in the repository, as
// paths relative to examples/demo. embed.FS reports every file as read-only, so
// the bit is restored here. A test compares this list with the repository's
// modes, so the two cannot drift.
var executable = map[string]bool{
	"global/skills/service-health/scripts/check.sh": true,
}

// Knowledge is the example content, laid out like a content directory. A file
// in executable reports 0755 and every other regular file 0644, like a
// directory Source.
func Knowledge() fs.FS {
	sub, err := fs.Sub(demo, "demo")
	if err != nil {
		panic(err) // "demo" is a valid path and is embedded
	}
	return modeFS{sub}
}

// modeFS gives the embedded files their modes.
type modeFS struct{ fs.FS }

func (m modeFS) Open(name string) (fs.File, error) {
	f, err := m.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return modeFile{f, name}, nil
}

func (m modeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(m.FS, name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, len(entries))
	for i, e := range entries {
		out[i] = modeEntry{e, path.Join(name, e.Name())}
	}
	return out, nil
}

type modeFile struct {
	fs.File
	name string
}

func (f modeFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return modeInfo{info, f.name}, nil
}

type modeEntry struct {
	fs.DirEntry
	name string
}

func (e modeEntry) Info() (fs.FileInfo, error) {
	info, err := e.DirEntry.Info()
	if err != nil {
		return nil, err
	}
	return modeInfo{info, e.name}, nil
}

type modeInfo struct {
	fs.FileInfo
	name string
}

func (i modeInfo) Mode() fs.FileMode {
	m := i.FileInfo.Mode()
	if !m.IsRegular() {
		return m
	}
	m = m&^fs.ModePerm | 0o644
	if executable[i.name] {
		m |= 0o111
	}
	return m
}
