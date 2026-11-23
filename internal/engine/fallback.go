package engine

import (
	"context"
	"io/fs"
)

// FSSource is a Source that reads from an fs.FS laid out like a content
// directory (global/articles/**, projects/**). It is how content embedded in
// the binary is served.
type FSSource struct {
	fsys fs.FS
}

var _ Source = FSSource{}

// NewFSSource returns a Source that reads fsys, which is the content root.
func NewFSSource(fsys fs.FS) FSSource { return FSSource{fsys: fsys} }

// Load reads the content in fsys into a Snapshot, or fails once ctx has ended.
func (s FSSource) Load(ctx context.Context) (*Snapshot, error) {
	if ctx.Err() != nil {
		return nil, errLoadCancelled
	}
	return loadFS(s.fsys)
}

// FallbackSource is a Source that serves primary, and fallback only while
// primary has no content at all. The two are never mixed.
type FallbackSource struct {
	primary, fallback Source
	used              func()
}

var _ Source = FallbackSource{}

// NewFallbackSource returns a Source over primary that loads fallback instead
// when primary's Snapshot holds no Items and no Projects. used, when not nil,
// is called each time the fallback is served.
func NewFallbackSource(primary, fallback Source, used func()) FallbackSource {
	return FallbackSource{primary: primary, fallback: fallback, used: used}
}

// Load loads primary, and fallback when primary is empty. A primary that fails
// to load fails the Load.
func (s FallbackSource) Load(ctx context.Context) (*Snapshot, error) {
	snap, err := s.primary.Load(ctx)
	if err != nil || len(snap.index) > 0 || len(snap.projects) > 0 {
		return snap, err
	}
	if s.used != nil {
		s.used()
	}
	return s.fallback.Load(ctx)
}
