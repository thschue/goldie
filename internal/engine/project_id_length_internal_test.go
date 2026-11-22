package engine

import (
	"strings"
	"testing"
	"testing/fstest"
)

// The ProjectId schema caps an id at 512 bytes. A Source whose host/org/repo
// is longer would be listed but never selectable, so loading refuses it.
func TestLoadRejectsOverLongProjectID(t *testing.T) {
	const article = "---\nname: X\nkeywords: [x]\n---\nbody\n"
	long := "projects/github.com/" + strings.Repeat("a", 250) + "/" + strings.Repeat("b", 251)
	fsys := fstest.MapFS{long + "/articles/x.md": {Data: []byte(article)}}
	if _, err := loadFS(fsys); err == nil || !strings.Contains(err.Error(), "512") {
		t.Fatalf("loadFS accepted a %d-byte Project id: %v", len(long)-len("projects/"), err)
	}

	ok := "projects/github.com/" + strings.Repeat("a", 250) + "/" + strings.Repeat("b", 200)
	fsys = fstest.MapFS{ok + "/articles/x.md": {Data: []byte(article)}}
	if _, err := loadFS(fsys); err != nil {
		t.Fatalf("loadFS rejected a Project id under the cap: %v", err)
	}
}
