package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Acceptance tests for #608 (slice 3 of #17): goldie loops list and goldie
// loops show <name>, following goldie agents list|show, with text and --json
// output. show prints the loop and its resolved closure (the agents and skills
// it names, with the Scope that answered for each). The CLI resolves nothing
// itself, so the stub cases pin what it asks for and how it words hostile
// server text, and the real-server cases run against the example content (the
// nightly loop, which names the reviewer agent). Pattern:
// cli_skills_agents_test.go.
//
// The exact wording of the text output is not fixed by the issue, so the
// tests pin the facts a reader needs, not their layout.

const (
	lpNightly = `{"name":"nightly","description":"Keep the nightly build green","scope":"global","interval":"30m","dynamic":false,` +
		`"stop":"when CI is green","content":"Check the build, fix what is red.\nReport what you changed.\n",` +
		`"closure":{"agents":[{"name":"reviewer","scope":"global"}],"skills":[{"name":"deploy","scope":"github.com/acme/shop"}]},` +
		`"digest":"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"}`
	lpPacer = `{"name":"pacer","description":"Paces itself","scope":"global","dynamic":true,"stop":"when the queue is empty",` +
		`"content":"Take the next item.\n","closure":{"agents":[],"skills":[]},"digest":"abc123"}`
	// A list answer carries no content.
	lpList = `{"loops":[` +
		`{"name":"nightly","description":"Keep the nightly build green","scope":"global","interval":"30m","dynamic":false,"stop":"when CI is green",` +
		`"closure":{"agents":[{"name":"reviewer","scope":"global"}],"skills":[]},"digest":"d1"},` +
		`{"name":"pacer","description":"Paces itself","scope":"global","dynamic":true,"stop":"when the queue is empty",` +
		`"closure":{"agents":[],"skills":[]},"digest":"d2"}]}`
)

func lpAnswers() map[string]string {
	return map[string]string{
		"/api/v1/loops":         lpList,
		"/api/v1/loops/nightly": lpNightly,
		"/api/v1/loops/pacer":   lpPacer,
	}
}

func TestCLILoopsListShowsEveryLoopWithItsDescription(t *testing.T) {
	addr, rec := saServer(t, lpAnswers())
	out, err := saRun(t, addr, "loops", "list")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "loops list", out, "nightly", "Keep the nightly build green", "pacer", "Paces itself", "goldie loops show")
	if !inOrder(out, "nightly", "pacer") {
		t.Errorf("loops list = %q, want the loops in the order the server sent them (sorted by name)", out)
	}
	if reqs := rec.requests(); len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].URL.Path != "/api/v1/loops" {
		t.Errorf("loops list made requests %v, want one GET /api/v1/loops", reqs)
	}
}

func TestCLILoopsListJSONIsTheAPIAnswer(t *testing.T) {
	addr, _ := saServer(t, lpAnswers())
	out, err := saRun(t, addr, "loops", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Loops []struct {
			Name, Description string
			Closure           struct{ Agents []struct{ Name string } }
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("loops list --json is not JSON: %v\n%q", err, out)
	}
	if len(got.Loops) != 2 || got.Loops[0].Name != "nightly" || got.Loops[1].Name != "pacer" || got.Loops[0].Description != "Keep the nightly build green" {
		t.Fatalf("loops = %+v, want nightly then pacer", got.Loops)
	}
	if a := got.Loops[0].Closure.Agents; len(a) != 1 || a[0].Name != "reviewer" {
		t.Errorf("nightly closure agents = %+v, want reviewer", a)
	}
}

func TestCLILoopsListOfNoLoopsIsNotAnError(t *testing.T) {
	addr, _ := saServer(t, map[string]string{"/api/v1/loops": `{"loops":[]}`})
	out, err := saRun(t, addr, "loops", "list")
	if err != nil {
		t.Fatalf("an empty list must not be an error: %v", err)
	}
	requireContains(t, "empty loops list", out, "No loops", "goldie status")
	jsonOut, err := saRun(t, addr, "loops", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Loops []any }
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil || got.Loops == nil || len(got.Loops) != 0 {
		t.Errorf("loops list --json = %q (%v), want an empty loops array", jsonOut, err)
	}
}

func TestCLILoopsShowPrintsTheLoopAndItsResolvedClosure(t *testing.T) {
	addr, rec := saServer(t, lpAnswers())
	out, err := saRun(t, addr, "loops", "show", "nightly")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "loops show nightly", out,
		"nightly", "Keep the nightly build green", "30m", "when CI is green",
		"Check the build, fix what is red.\nReport what you changed.",
		"reviewer", "deploy")
	if strings.Contains(out, "pacer") {
		t.Errorf("loops show nightly = %q, must show only that loop", out)
	}
	if reqs := rec.requests(); len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].URL.Path != "/api/v1/loops/nightly" {
		t.Errorf("loops show made requests %v, want one GET /api/v1/loops/nightly", reqs)
	}
}

func TestCLILoopsShowOfADynamicLoopSaysSo(t *testing.T) {
	addr, _ := saServer(t, lpAnswers())
	out, err := saRun(t, addr, "loops", "show", "pacer")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "loops show pacer", out, "pacer", "Paces itself", "dynamic", "when the queue is empty", "Take the next item.")
}

func TestCLILoopsShowJSONIsTheAPIAnswer(t *testing.T) {
	addr, _ := saServer(t, lpAnswers())
	out, err := saRun(t, addr, "loops", "show", "nightly", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Name, Description, Interval, Stop, Content, Digest string
		Dynamic                                            bool
		Closure                                            struct {
			Agents, Skills []struct{ Name, Scope string }
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("loops show --json is not JSON: %v\n%q", err, out)
	}
	if got.Name != "nightly" || got.Interval != "30m" || got.Dynamic || got.Stop != "when CI is green" ||
		!strings.HasPrefix(got.Content, "Check the build") || !strings.HasPrefix(got.Digest, "9f86d081") {
		t.Errorf("loops show --json = %+v, want the API answer", got)
	}
	if len(got.Closure.Agents) != 1 || got.Closure.Agents[0].Name != "reviewer" ||
		len(got.Closure.Skills) != 1 || got.Closure.Skills[0].Name != "deploy" || got.Closure.Skills[0].Scope != "github.com/acme/shop" {
		t.Errorf("closure = %+v, want agent reviewer and skill deploy (scope github.com/acme/shop)", got.Closure)
	}
}

// The commands against a real server over the example content, so the CLI and
// the API agree on the shapes.
func TestCLILoopsReadTheExampleContent(t *testing.T) {
	srv := cliServer(t)
	cases := []struct {
		args  []string
		wants []string
	}{
		{[]string{"loops", "list"}, []string{"nightly", "Keep the nightly build green"}},
		{[]string{"loops", "show", "nightly"}, []string{"nightly", "30m", "when CI is green", "Check the nightly build and fix what broke.", "reviewer"}},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			out, err := saRun(t, srv.URL, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			requireContains(t, strings.Join(c.args, " "), out, c.wants...)
			jsonOut, err := saRun(t, srv.URL, append(c.args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid([]byte(jsonOut)) {
				t.Errorf("%v --json is not JSON: %q", c.args, jsonOut)
			}
		})
	}
}

// Local mode: --dir starts an embedded server over the content, so no token
// and no address are needed.
func TestCLILoopsReadLocalContentWithDir(t *testing.T) {
	dir := dirContent(t)
	for args, want := range map[string]string{"loops list": "nightly", "loops show nightly": "Check the nightly build"} {
		out, err := runCLI(t, append(strings.Fields(args), "--dir", dir)...)
		if err != nil {
			t.Fatalf("%s --dir: %v", args, err)
		}
		requireContains(t, args+" --dir", out, want)
	}
}

func TestCLILoopsAskInTheProject(t *testing.T) {
	addr, rec := saServer(t, lpAnswers())
	const project = "github.com/acme/shop"
	for _, args := range [][]string{{"loops", "list"}, {"loops", "show", "nightly"}} {
		if _, err := saRun(t, addr, append(args, "--project", project)...); err != nil {
			t.Fatalf("%v --project: %v", args, err)
		}
	}
	reqs := rec.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	for _, r := range reqs {
		if got := r.URL.Query().Get("project"); got != project {
			t.Errorf("%s asked with project=%q, want %q", r.URL.Path, got, project)
		}
	}
}

func TestCLILoopsSendTheTokenAsBearer(t *testing.T) {
	addr, rec := saServer(t, lpAnswers())
	if _, err := saRun(t, addr, "loops", "list"); err != nil {
		t.Fatal(err)
	}
	if _, err := saRun(t, addr, "loops", "show", "nightly"); err != nil {
		t.Fatal(err)
	}
	reqs := rec.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	for _, r := range reqs {
		if got := r.Header.Get("Authorization"); got != "Bearer "+cliToken {
			t.Errorf("%s sent Authorization %q, want the bearer token", r.URL.Path, got)
		}
	}
}

func TestCLILoopsWithoutATokenFailBeforeAnyRequest(t *testing.T) {
	addr, rec := saServer(t, lpAnswers())
	for _, args := range [][]string{{"loops", "list"}, {"loops", "show", "nightly"}} {
		out, err := runCLI(t, append(args, "--addr", addr)...)
		if err == nil || !strings.Contains(err.Error(), "no token") {
			t.Errorf("%v without a token: err = %v, want the no-token error", args, err)
		}
		if out != "" {
			t.Errorf("%v without a token printed %q", args, out)
		}
	}
	if n := len(rec.requests()); n != 0 {
		t.Errorf("made %d requests without a token, want none", n)
	}
}

// A miss is an error that prints nothing on stdout, names the list command,
// and never echoes the address or token.
func TestCLILoopsShowOfAnUnknownNameFailsAndNamesTheListCommand(t *testing.T) {
	addr, _ := saServer(t, map[string]string{})
	out, err := saRun(t, addr, "loops", "show", "nope")
	if err == nil {
		t.Fatal("loops show nope must fail")
	}
	if out != "" {
		t.Errorf("loops show nope printed %q on a miss", out)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want the status", err)
	}
	for _, bad := range []string{addr, strings.TrimPrefix(addr, "http://"), cliToken} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error carries %q: %q", bad, err)
		}
	}

	plain := hostileServer(t, http.StatusNotFound, `{"error":{"code":"not_found","message":"no such loop","details":{"hint":"List the loops, then use a name from the list."}}}`)
	_, err = saRun(t, plain, "loops", "show", "nope")
	if err == nil || !strings.Contains(err.Error(), "Hint: List the loops with: goldie loops list, then use a name from its results.") {
		t.Errorf("error = %v, want the hint to name goldie loops list", err)
	}
	other := hostileServer(t, http.StatusNotFound, `{"error":{"code":"not_found","message":"no such loop","details":{"hint":"Some other hint."}}}`)
	_, err = saRun(t, other, "loops", "show", "nope")
	if err == nil || !strings.Contains(err.Error(), "Hint: Some other hint.") {
		t.Errorf("error = %v, want another server hint unchanged", err)
	}
}

func TestCLILoopsWithABadTokenFail(t *testing.T) {
	addr := hostileServer(t, http.StatusUnauthorized, `{"error":{"code":"unauthorized","message":"bad token","details":{}}}`)
	for _, args := range [][]string{{"loops", "list"}, {"loops", "show", "x"}} {
		out, err := saRun(t, addr, args...)
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("%v: err = %v, want the 401", args, err)
		}
		if out != "" {
			t.Errorf("%v printed %q", args, out)
		}
	}
}

// Wrong use is an error that makes no request.
func TestCLILoopsMisuseMakesNoRequest(t *testing.T) {
	addr, rec := saServer(t, lpAnswers())
	for _, args := range [][]string{
		{"loops"}, {"loops", "frobnicate"}, {"loops", "show"}, {"loops", "show", "a", "b"}, {"loops", "list", "extra"},
		{"loops", "show", "nightly", "--file", "x"},
	} {
		out, err := saRun(t, addr, args...)
		if err == nil {
			t.Errorf("%v must fail", args)
		}
		if strings.Contains(out, "nightly") {
			t.Errorf("%v printed content: %q", args, out)
		}
	}
	if n := len(rec.requests()); n != 0 {
		t.Errorf("made %d requests for wrong use, want none", n)
	}
	// Control: right use asks the server.
	if _, err := saRun(t, addr, "loops", "list"); err != nil {
		t.Fatalf("loops list: %v", err)
	}
	if n := len(rec.requests()); n != 1 {
		t.Errorf("right use made %d requests, want 1", n)
	}
}

// A name that URL path cleaning would resolve into another route is refused
// before any request; one with a slash stays one path segment.
func TestCLILoopsShowKeepsTheNameOneSegment(t *testing.T) {
	ctlAddr, ctl := saServer(t, lpAnswers())
	if _, err := saRun(t, ctlAddr, "loops", "show", "nightly"); err != nil {
		t.Fatalf("loops show nightly: %v", err)
	}
	if n := len(ctl.requests()); n != 1 {
		t.Fatalf("loops show nightly made %d requests, want 1", n)
	}
	for _, name := range []string{"", ".", ".."} {
		addr, rec := saServer(t, lpAnswers())
		if _, err := saRun(t, addr, "loops", "show", name); err == nil {
			t.Errorf("loops show %q must fail", name)
		}
		if n := len(rec.requests()); n != 0 {
			t.Errorf("loops show %q made %d requests, want none", name, n)
		}
	}
	for _, name := range []string{"a/b", "../agents/reviewer", "x?project=evil", "x#frag", "a%2Fb"} {
		addr, rec := saServer(t, lpAnswers())
		_, _ = saRun(t, addr, "loops", "show", name)
		for _, r := range rec.requests() {
			if want := "/api/v1/loops/" + url.PathEscape(name); r.URL.EscapedPath() != want {
				t.Errorf("loops show %q requested %q, want %q", name, r.URL.EscapedPath(), want)
			}
			if r.URL.RawQuery != "" {
				t.Errorf("loops show %q sent the query %q", name, r.URL.RawQuery)
			}
		}
	}
}

// Server text is untrusted: format characters and terminal escapes are
// cleaned in the text output, and escaped without changing the value in
// --json. The description, stop and content are cleaned as text; a name and a
// closure name as one line, so none can forge a line of its own.
func TestCLILoopsDropFormatCharactersAndEscapesFromServerText(t *testing.T) {
	h := jsonString(t, hiddenCf)
	esc := jsonString(t, "x\x1b[2Jy\u202ez\u2028w")
	hint := `"hint":` + h
	loop := `{"name":` + h + `,"description":` + jsonString(t, "d "+hiddenCf+"\nnext "+hiddenCf) + `,"dynamic":true,"stop":` + esc +
		`,"content":` + jsonString(t, "first "+hiddenCf+"\nsecond "+hiddenCf+"\x1b[31m") +
		`,"closure":{"agents":[{"name":` + h + `,"scope":` + h + `}],"skills":[{"name":` + jsonString(t, "s\nFORGED"+hiddenCf) + `,"scope":"global"}]},"digest":"d",` + hint + `}`
	addr, _ := saServer(t, map[string]string{
		"/api/v1/loops":   `{"loops":[` + loop + `],` + hint + `}`,
		"/api/v1/loops/x": loop,
	})
	for _, args := range [][]string{{"loops", "list"}, {"loops", "show", "x"}} {
		out, err := saRun(t, addr, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if leaked := formatChars(out); len(leaked) > 0 {
			t.Errorf("%v output carries format characters %v:\n%q", args, leaked, out)
		}
		for _, bad := range []string{"\x1b", "\u2028", "\u202e"} {
			if strings.Contains(out, bad) {
				t.Errorf("%v output carries %q:\n%q", args, bad, out)
			}
		}
		requireContains(t, strings.Join(args, " "), out, "abcdefghijk")
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "FORGED") {
				t.Errorf("%v: a closure name forged the line %q", args, line)
			}
		}
	}
	jsonOut, err := saRun(t, addr, "loops", "show", "x", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(jsonOut); len(leaked) > 0 {
		t.Errorf("loops show --json carries raw format characters %v:\n%q", leaked, jsonOut)
	}
	var got struct{ Name string }
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil || got.Name != hiddenCf {
		t.Errorf("loops show --json name = %q (%v), want the value unchanged", got.Name, err)
	}
}

// A server that answers with something that is not the API (a proxy's login
// page) is an error, not output.
func TestCLILoopsOfANonAPIAnswerFailsWithoutOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>login</html>"))
	}))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{{"loops", "list"}, {"loops", "show", "nightly"}} {
		out, err := saRun(t, srv.URL, args...)
		if err == nil || out != "" {
			t.Errorf("%v: out = %q, err = %v, want an error and no output", args, out, err)
		}
	}
}
