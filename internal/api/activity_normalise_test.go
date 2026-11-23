package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #325: recording uses the checked Project id, is not a
// call that has to precede Register, and every string field of an Event is
// bounded by one shared step.

// oversizedEvent has every string field a peer or caller controls far over
// maxQueryBytes, in multi-byte runes so a cut inside one shows.
func oversizedEvent() Event {
	long := strings.Repeat("€", 5*maxQueryBytes)
	return Event{Kind: EventKind(long), Query: long, Token: long, ClientVersion: long, Project: long, TraceID: long}
}

func assertEveryFieldBounded(t *testing.T, path string, e Event) {
	t.Helper()
	want := truncateQuery(strings.Repeat("€", 5*maxQueryBytes))
	for name, v := range map[string]string{
		"Kind": string(e.Kind), "Query": e.Query, "Token": e.Token,
		"ClientVersion": e.ClientVersion, "Project": e.Project, "TraceID": e.TraceID,
	} {
		if len(v) > maxQueryBytes || !utf8.ValidString(v) || v != want {
			t.Errorf("%s: %s is %d bytes (valid %v), want the %d-byte whole-rune prefix", path, name, len(v), utf8.ValidString(v), len(want))
		}
	}
}

func TestActivityRecordBoundsEveryStringFieldOfAnEvent(t *testing.T) {
	a, _ := newTestActivity(4)
	_, live, cancel := a.Subscribe()
	defer cancel()
	a.Record(oversizedEvent())
	history, _, c2 := a.Subscribe()
	defer c2()
	assertEveryFieldBounded(t, "history", history[0])
	assertEveryFieldBounded(t, "live", <-live)
}

func TestMergerHistoryAndNormalizeEventBoundEveryStringFieldOfAnEvent(t *testing.T) {
	e := oversizedEvent()
	e.Replica, e.ID, e.Time = "a", 1, mergeT0
	got := startMerger(t, MergeConfig{}, MergeInput{Name: "a", History: []Event{e}}).hist
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	assertEveryFieldBounded(t, "merger history", got[0])
	n, ok := normalizeEvent(e)
	if !ok {
		t.Fatal("normalizeEvent rejected a valid, oversized event")
	}
	assertEveryFieldBounded(t, "normalizeEvent", n)
}

// Both paths leave a field within the bound untouched.
func TestActivityBoundsLeaveShortFieldsUntouched(t *testing.T) {
	e := Event{Kind: KindLookup, Query: "q", Token: "ci", ClientVersion: "1.5.0", Project: "Shop", TraceID: "abc", Miss: true}
	a, _ := newTestActivity(4)
	a.Record(e)
	history, _, cancel := a.Subscribe()
	defer cancel()
	got := history[0]
	if got.Kind != e.Kind || got.Query != e.Query || got.Token != e.Token || got.ClientVersion != e.ClientVersion ||
		got.Project != e.Project || got.TraceID != e.TraceID || !got.Miss {
		t.Errorf("Record changed a short event: %+v", got)
	}
	e.Replica, e.ID = "a", 1
	n, ok := normalizeEvent(e)
	if !ok || n.Kind != e.Kind || n.Query != e.Query || n.Token != e.Token || n.ClientVersion != e.ClientVersion ||
		n.Project != e.Project || n.TraceID != e.TraceID || !n.Miss {
		t.Errorf("normalizeEvent changed a short event: %+v (ok %v)", n, ok)
	}
}

// A Project id that fails validProject is never recorded as sent.
func TestRecordedDropsAProjectIDTheCheckRejects(t *testing.T) {
	for name, bad := range map[string]string{
		"markup":     "<b>x</b>",
		"whitespace": "my project",
		"newline":    "a\nb",
		"oversized":  strings.Repeat("p", 5*maxQueryBytes),
		"bidi":       "a‮b",
	} {
		p := contract.ProjectId(bad)
		if _, ok := validProject(&p); ok {
			t.Fatalf("%s: fixture is a valid ProjectId; fix the test", name)
		}
		e, ok := recorded(contract.SearchArticlesRequestObject{Params: contract.SearchArticlesParams{Project: &p}},
			contract.SearchArticles200JSONResponse{Query: "q"})
		if !ok || e.Project != "" {
			t.Errorf("%s: search Project = %.20q (recorded %v), want empty", name, e.Project, ok)
		}
		e, ok = recorded(contract.GetArticleRequestObject{Id: "a", Params: contract.GetArticleParams{Project: &p}},
			contract.GetArticle200JSONResponse{})
		if !ok || e.Project != "" {
			t.Errorf("%s: lookup Project = %.20q (recorded %v), want empty", name, e.Project, ok)
		}
	}
	good := contract.ProjectId("github.com/acme/Shop")
	if e, _ := recorded(contract.SearchArticlesRequestObject{Params: contract.SearchArticlesParams{Project: &good}},
		contract.SearchArticles200JSONResponse{}); e.Project != "github.com/acme/Shop" {
		t.Errorf("valid Project = %q, want github.com/acme/Shop", e.Project)
	}
}

func parseAPIFile(t *testing.T, name string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// One parse path: sentProject goes through validProject.
func TestSentProjectCallsValidProject(t *testing.T) {
	_, _, funcs, _ := apiDecls(t)
	d := funcs["sentProject"]
	if d == nil {
		t.Skip("sentProject is gone; TestRecordedDropsAProjectIDTheCheckRejects covers the behaviour")
	}
	called := false
	ast.Inspect(d.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "validProject" {
				called = true
			}
		}
		return true
	})
	if !called {
		t.Error("sentProject does not call validProject")
	}
}

// Recording is chosen when the Server is built, not by a method that has to
// run before Register.
func TestServerHasNoRecordIntoMethod(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		for _, d := range parseAPIFile(t, e.Name()).Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && fd.Name.Name == "RecordInto" {
				t.Errorf("%s declares (*Server).RecordInto; recording must be a NewServer option or argument", e.Name())
			}
		}
	}
}

// The MaxActivityCapacity comment states the worst case of a full ring: about
// 12 to 15 MiB with 1,024-byte queries, not the 2 MiB of short ones.
func TestMaxActivityCapacityCommentStatesTheWorstCase(t *testing.T) {
	f := parseAPIFile(t, "activity.go")
	var doc string
	ast.Inspect(f, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok {
			for _, id := range vs.Names {
				if id.Name == "MaxActivityCapacity" && vs.Doc != nil {
					doc = vs.Doc.Text()
				}
			}
		}
		return true
	})
	if doc == "" {
		t.Fatal("MaxActivityCapacity has no doc comment")
	}
	flat := strings.Join(strings.Fields(doc), " ")
	if !regexp.MustCompile(`\b1[2-5](\.\d+)? ?MiB`).MatchString(flat) {
		t.Errorf("comment does not state a 12 to 15 MiB worst case: %q", flat)
	}
	if !strings.Contains(flat, "1,024") && !strings.Contains(flat, "1024") {
		t.Errorf("comment does not name the 1,024-byte query it assumes: %q", flat)
	}
}

// The garbled doc line in record.go is reworded to say "does not match".
func TestRecordGoDocSaysDoesNotMatch(t *testing.T) {
	b, err := os.ReadFile("record.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "does not match") {
		t.Error(`record.go has no "does not match" in its docs`)
	}
	if strings.Contains(string(b), "a type recorded leaves out") {
		t.Error("record.go still has the garbled line (a type recorded leaves out)")
	}
}
