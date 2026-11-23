package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"runtime"
	"strings"
	"testing"
)

// Acceptance tests for #242 (with #263 and #310 folded in) that hold inside
// package api. They read the package's own source where the criterion is a
// property of the code, as TestAppHasNoOptionsCopyOrTestOnlyConstructors does
// in internal/app, so nothing here fails to compile before the change lands.

// apiDecls parses the package's non-test files and returns its top-level
// functions and types by name, with the file set for positions.
func apiDecls(t *testing.T) (*token.FileSet, []*ast.File, map[string]*ast.FuncDecl, map[string]*ast.TypeSpec) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	funcs, types := map[string]*ast.FuncDecl{}, map[string]*ast.TypeSpec{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					funcs[d.Name.Name] = d
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					if ts, ok := s.(*ast.TypeSpec); ok {
						types[ts.Name.Name] = ts
					}
				}
			}
		}
	}
	return fset, files, funcs, types
}

// typeName is a parameter or result type as written, with one pointer
// stripped: "checkedProject", "contract.ProjectId", "string".
func typeName(e ast.Expr) string {
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	}
	return "?"
}

// fieldTypes lists the type of every parameter (or result) in fl, one entry
// per name.
func fieldTypes(fl *ast.FieldList) []string {
	if fl == nil {
		return nil
	}
	var out []string
	for _, f := range fl.List {
		n := max(len(f.Names), 1)
		for range n {
			out = append(out, typeName(f.Type))
		}
	}
	return out
}

// Criterion (#263): validProject returns the checked id as its own type, and
// scopeHint and unregisteredHint accept only that type, so an id no check has
// passed cannot reach the hint text. The expected shape, with any name for the
// type (checkedProject, say):
//
//	type checkedProject ...                                   // a defined type, not an alias
//	func validProject(project *contract.ProjectId) (checkedProject, bool)
//	func scopeHint(snap *engine.Snapshot, project checkedProject) (proj string, hint *string)
//	func unregisteredHint(id checkedProject) string
//
// A pointer to the type is accepted wherever the type is.
func TestHintsAcceptOnlyTheCheckedProjectType(t *testing.T) {
	fset, _, funcs, types := apiDecls(t)
	for _, name := range []string{"validProject", "scopeHint", "unregisteredHint"} {
		if funcs[name] == nil {
			t.Fatalf("package api declares no func %s", name)
		}
	}
	results := fieldTypes(funcs["validProject"].Type.Results)
	if len(results) == 0 {
		t.Fatalf("%s: validProject returns nothing, want the checked id first", fset.Position(funcs["validProject"].Pos()))
	}
	checked := results[0]
	ts := types[checked]
	switch {
	case ts == nil:
		t.Fatalf("%s: validProject returns %v; want the checked id first, as a type declared in package api (bool, string and contract.ProjectId are not one)",
			fset.Position(funcs["validProject"].Pos()), results)
	case ts.Assign.IsValid():
		t.Fatalf("%s: %s is an alias; want a defined type, so an unchecked id does not convert to it silently", fset.Position(ts.Pos()), checked)
	}
	unchecked := map[string]bool{"string": true, "contract.ProjectId": true, "any": true}
	for _, name := range []string{"scopeHint", "unregisteredHint"} {
		params := fieldTypes(funcs[name].Type.Params)
		n := 0
		for _, p := range params {
			if p == checked {
				n++
			}
			if unchecked[p] {
				t.Errorf("%s: %s takes a %s; want it to accept only %s", fset.Position(funcs[name].Pos()), name, p, checked)
			}
		}
		if n != 1 {
			t.Errorf("%s: %s params %v, want exactly one %s", fset.Position(funcs[name].Pos()), name, params, checked)
		}
	}
	if params := fieldTypes(funcs["unregisteredHint"].Type.Params); len(params) != 1 {
		t.Errorf("unregisteredHint params %v, want only the checked id", params)
	}
}

// Criterion: no OpenTelemetry type is used in api (#13 decision). trace_id
// comes from platform.TraceID; api imports nothing from go.opentelemetry.io.
func TestAPIImportsNoOpenTelemetry(t *testing.T) {
	fset, files, _, _ := apiDecls(t)
	if len(files) == 0 {
		t.Fatal("no source files read")
	}
	for _, f := range files {
		for _, imp := range f.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); strings.HasPrefix(p, "go.opentelemetry.io/") {
				t.Errorf("%s: api imports %s; take the trace id from platform.TraceID", fset.Position(imp.Pos()), p)
			}
		}
	}
}

// Criterion (#310): the capacity Activity keeps is bounded. NewActivity
// clamps an oversized capacity to a sane maximum, or refuses it with a nil
// (disabled) Activity; either way it never panics and never allocates what
// the caller asked for. 16 MiB is far above any sane ring (10,000 events are
// about 2 MiB) and far below the 1<<18 events asked for here.
func TestNewActivityBoundsAnOversizedCapacity(t *testing.T) {
	const asked = 1 << 18
	const budget = 16 << 20
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	a := NewActivity(asked, "replica-a", nil)
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > budget {
		t.Errorf("NewActivity(%d) allocated %d bytes, want at most %d: the capacity is not clamped", asked, grew, budget)
	}
	if a == nil {
		return // refused: a nil Activity is disabled
	}
	recordN(a, asked+1)
	history, _, cancel := a.Subscribe()
	defer cancel()
	if len(history) == 0 || len(history) >= asked {
		t.Errorf("NewActivity(%d) kept %d of %d events, want it clamped below %d (or refused)", asked, len(history), asked+1, asked)
	}
}

// The largest int cannot be allocated at all: NewActivity clamps or refuses
// it instead of panicking at server wiring.
func TestNewActivityDoesNotPanicOnTheLargestCapacity(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("NewActivity(math.MaxInt) panicked: %v; want it clamped or refused", r)
		}
	}()
	a := NewActivity(math.MaxInt, "replica-a", nil)
	a.Record(Event{Kind: KindSearch, Query: "q"})
	_, _, cancel := a.Subscribe()
	cancel()
}

// The clamp is a ceiling, not a new size: an ordinary capacity is kept as
// given.
func TestNewActivityKeepsAnOrdinaryCapacity(t *testing.T) {
	const capacity = 1000
	a, _ := newTestActivity(capacity)
	recordN(a, capacity+10)
	history, _, cancel := a.Subscribe()
	defer cancel()
	if len(history) != capacity {
		t.Errorf("NewActivity(%d) kept %d events, want %d", capacity, len(history), capacity)
	}
}
