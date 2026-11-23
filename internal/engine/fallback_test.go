package engine

import (
	"context"
	"testing"
	"testing/fstest"
)

func fallbackFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

const fallbackArticle = "---\nname: A\nkeywords: [a]\n---\nBody.\n"

func TestFallbackSourceServesOnlyTheFallbackWhenThePrimaryIsEmpty(t *testing.T) {
	calls := 0
	src := NewFallbackSource(NewFSSource(fallbackFS(nil)),
		NewFSSource(fallbackFS(map[string]string{"global/articles/ex.md": fallbackArticle})), func() { calls++ })
	snap, err := src.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Article("ex"); !ok || calls != 1 {
		t.Errorf("fallback article found = %v, used called %d times, want true and 1", ok, calls)
	}
}

func TestFallbackSourceNeverMixesInWhenThePrimaryHasContent(t *testing.T) {
	calls := 0
	src := NewFallbackSource(NewFSSource(fallbackFS(map[string]string{"global/articles/mine.md": fallbackArticle})),
		NewFSSource(fallbackFS(map[string]string{"global/articles/ex.md": fallbackArticle})), func() { calls++ })
	snap, err := src.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Article("mine"); !ok {
		t.Error("primary article missing")
	}
	if _, ok := snap.Article("ex"); ok || calls != 0 {
		t.Errorf("fallback leaked: found = %v, used called %d times", ok, calls)
	}
}
