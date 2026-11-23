package engine

import (
	"context"
	"strings"
	"testing"
)

// claimMemSource serves a prepared Snapshot, so a test can hold a Project that
// no directory Source loads.
type claimMemSource struct{ snap *Snapshot }

func (m claimMemSource) Load(context.Context) (*Snapshot, error) { return m.snap, nil }

// Issue #477: "github.com/acme/*" rejects github.com/acme/a/b. DirSource reads
// only projects/<host>/<org>/<repo>, so a Source holding a Project with an
// extra segment is built in memory here.
func TestClaimGlobRejectsProjectWithExtraSegment(t *testing.T) {
	const deep = "github.com/acme/a/b"
	const file = "projects/github.com/acme/a/b/articles/deep.md"
	snap := newSnapshot(nil)
	snap.addProject(deep, []Article{{ID: "deep", Scope: deep, Name: "Deep", file: file}})

	_, err := NewClaimedSource(claimMemSource{snap}, Claim{Projects: []string{"github.com/acme/*"}}).Load(t.Context())
	if err == nil {
		t.Fatalf("Load succeeded; github.com/acme/* must not accept %s", deep)
	}
	if !strings.Contains(err.Error(), file) {
		t.Errorf("error %q does not name %s", err, file)
	}

	// The same Project is accepted by a glob with a segment for each part.
	if _, err := NewClaimedSource(claimMemSource{snap}, Claim{Projects: []string{"github.com/acme/*/*"}}).Load(t.Context()); err != nil {
		t.Errorf("github.com/acme/*/*: %v", err)
	}
}
