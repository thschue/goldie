package engine

import (
	"reflect"
	"testing"
)

// TestSearchTieBreakAndRepeats builds the Snapshot out of ID order, so the ID
// tie-break is what puts tied hits in order, and repeats query words, so
// Matched must drop duplicates.
func TestSearchTieBreakAndRepeats(t *testing.T) {
	snap := &Snapshot{articles: []Article{
		{ID: "zeta", Name: "Zeta", Keywords: []string{"sql"}},
		{ID: "alpha", Name: "Alpha", Keywords: []string{"sql"}},
	}}
	type hit struct {
		id      string
		score   int
		matched []string
	}
	tests := []struct {
		name  string
		query string
		want  []hit
	}{
		{"ties sort by id", "sql", []hit{
			{"alpha", 4, []string{"sql"}},
			{"zeta", 4, []string{"sql"}},
		}},
		{"repeated words score twice, match once", "zeta zeta", []hit{
			{"zeta", 4, []string{"name"}},
		}},
		{"repeated keyword match once", "sql sql", []hit{
			{"alpha", 8, []string{"sql"}},
			{"zeta", 8, []string{"sql"}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []hit
			for _, h := range snap.Search(tt.query) {
				got = append(got, hit{h.ID, h.Score, h.Matched})
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Search(%q) = %+v, want %+v", tt.query, got, tt.want)
			}
		})
	}
}
