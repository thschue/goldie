package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Acceptance tests for #738 (part of #34): goldie decisions list and goldie
// decisions show <name>, following goldie loops (cli_loops_test.go). list
// prints the accepted decisions with name, title and Scope; show prints the
// body. A miss is reworded as for loops, with the same exit codes, and every
// printed value is cleaned of format characters and terminal escapes. The
// stub cases pin what the CLI asks for and how it words hostile server text;
// the real-server cases run against the test content (use-postgres).
//
// The exact layout of the text output is not fixed by the issue, so the
// tests pin the facts a reader needs, not their layout.

const (
	dcPostgres = `{"name":"use-postgres","title":"Use PostgreSQL for the shop database","status":"accepted","date":"2023-05-02",` +
		`"keywords":["database"],"scope":"global","supersedes":[],"superseded_by":"",` +
		`"content":"PostgreSQL is the default relational store.\nBackups run nightly.\n"}`
	dcQueue = `{"name":"use-queue","title":"Use a queue between the shop and billing","status":"accepted","date":"2024-01-09",` +
		`"keywords":[],"scope":"github.com/acme/shop","supersedes":[],"superseded_by":"","content":"Billing reads from the queue.\n"}`
	// A list answer carries no content.
	dcList = `{"decisions":[` +
		`{"name":"use-postgres","title":"Use PostgreSQL for the shop database","status":"accepted","date":"2023-05-02","keywords":["database"],"scope":"global","supersedes":[],"superseded_by":""},` +
		`{"name":"use-queue","title":"Use a queue between the shop and billing","status":"accepted","date":"2024-01-09","keywords":[],"scope":"github.com/acme/shop","supersedes":[],"superseded_by":""}]}`
)

func dcAnswers() map[string]string {
	return map[string]string{
		"/api/v1/decisions":              dcList,
		"/api/v1/decisions/use-postgres": dcPostgres,
		"/api/v1/decisions/use-queue":    dcQueue,
	}
}

func TestCLIDecisionsListPrintsNameTitleAndScopeOfEachDecision(t *testing.T) {
	addr, rec := saServer(t, dcAnswers())
	out, err := saRun(t, addr, "decisions", "list")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "decisions list", out,
		"use-postgres", "Use PostgreSQL for the shop database", "global",
		"use-queue", "Use a queue between the shop and billing", "github.com/acme/shop",
		"goldie decisions show")
	if !inOrder(out, "use-postgres", "use-queue") {
		t.Errorf("decisions list = %q, want the decisions in the order the server sent them (sorted by name)", out)
	}
	if strings.Contains(out, "Backups run nightly") {
		t.Errorf("decisions list = %q, must not print a body", out)
	}
	reqs := rec.requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].URL.Path != "/api/v1/decisions" {
		t.Fatalf("decisions list made requests %v, want one GET /api/v1/decisions", reqs)
	}
	// Only the accepted decisions: the server's default, or asked for by name.
	if s := reqs[0].URL.Query().Get("status"); s != "" && s != "accepted" {
		t.Errorf("decisions list asked for status=%q, want the accepted decisions", s)
	}
}

func TestCLIDecisionsListJSONIsTheAPIAnswer(t *testing.T) {
	addr, _ := saServer(t, dcAnswers())
	out, err := saRun(t, addr, "decisions", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Decisions []struct{ Name, Title, Scope string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decisions list --json is not JSON: %v\n%q", err, out)
	}
	if len(got.Decisions) != 2 || got.Decisions[0].Name != "use-postgres" || got.Decisions[0].Scope != "global" ||
		got.Decisions[1].Title != "Use a queue between the shop and billing" {
		t.Errorf("decisions = %+v, want use-postgres then use-queue", got.Decisions)
	}
}

func TestCLIDecisionsListOfNoDecisionsIsNotAnError(t *testing.T) {
	addr, _ := saServer(t, map[string]string{"/api/v1/decisions": `{"decisions":[]}`})
	out, err := saRun(t, addr, "decisions", "list")
	if err != nil {
		t.Fatalf("an empty list must not be an error: %v", err)
	}
	requireContains(t, "empty decisions list", out, "No decisions")
}

func TestCLIDecisionsShowPrintsTheBody(t *testing.T) {
	addr, rec := saServer(t, dcAnswers())
	out, err := saRun(t, addr, "decisions", "show", "use-postgres")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "decisions show use-postgres", out,
		"use-postgres", "Use PostgreSQL for the shop database", "global",
		"PostgreSQL is the default relational store.\nBackups run nightly.")
	if strings.Contains(out, "use-queue") {
		t.Errorf("decisions show use-postgres = %q, must show only that decision", out)
	}
	if reqs := rec.requests(); len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].URL.Path != "/api/v1/decisions/use-postgres" {
		t.Errorf("decisions show made requests %v, want one GET /api/v1/decisions/use-postgres", reqs)
	}
}

func TestCLIDecisionsShowJSONIsTheAPIAnswer(t *testing.T) {
	addr, _ := saServer(t, dcAnswers())
	out, err := saRun(t, addr, "decisions", "show", "use-postgres", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Name, Title, Scope, Status, Content string }
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decisions show --json is not JSON: %v\n%q", err, out)
	}
	if got.Name != "use-postgres" || got.Scope != "global" || got.Status != "accepted" || !strings.HasPrefix(got.Content, "PostgreSQL is the default") {
		t.Errorf("decisions show --json = %+v, want the API answer", got)
	}
}

// The commands against a real server over the test content, so the CLI and
// the API agree on the shapes.
func TestCLIDecisionsReadTheContent(t *testing.T) {
	// dirContent is absolute but resolves against the working directory,
	// which every runner changes, so it is read before the first run.
	dir := dirContent(t)
	srv := cliServer(t)
	out, err := saRun(t, srv.URL, "decisions", "list")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "decisions list", out, "use-postgres", "Use PostgreSQL for the shop database", "global")
	out, err = saRun(t, srv.URL, "decisions", "show", "use-postgres")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "decisions show use-postgres", out, "PostgreSQL is the default relational store.")
	// Local mode: --dir starts an embedded server, no token and no address.
	out, err = runCLI(t, "decisions", "show", "use-postgres", "--dir", dir)
	if err != nil {
		t.Fatalf("decisions show --dir: %v", err)
	}
	requireContains(t, "decisions show --dir", out, "PostgreSQL is the default relational store.")
}

func TestCLIDecisionsAskInTheProjectWithTheBearerToken(t *testing.T) {
	addr, rec := saServer(t, dcAnswers())
	const project = "github.com/acme/shop"
	for _, args := range [][]string{{"decisions", "list"}, {"decisions", "show", "use-queue"}} {
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
		if got := r.Header.Get("Authorization"); got != "Bearer "+cliToken {
			t.Errorf("%s sent Authorization %q, want the bearer token", r.URL.Path, got)
		}
	}
}

// Server text is untrusted: format characters and terminal escapes are
// cleaned in the text output of both commands, and each printed value stays
// on its own line.
func TestCLIDecisionsDropFormatCharactersAndEscapesFromServerText(t *testing.T) {
	h := jsonString(t, hiddenCf)
	esc := jsonString(t, "x\x1b[2Jy\u202ez\u2028w")
	hint := `"hint":` + h
	dec := `{"name":` + h + `,"title":` + jsonString(t, "t "+hiddenCf+"\nFORGED title") + `,"status":"accepted","date":"2023-05-02","keywords":[],` +
		`"scope":` + esc + `,"supersedes":[],"superseded_by":"",` +
		`"content":` + jsonString(t, "first "+hiddenCf+"\nsecond "+hiddenCf+"\x1b[31m") + `,` + hint + `}`
	addr, _ := saServer(t, map[string]string{
		"/api/v1/decisions":   `{"decisions":[` + dec + `],` + hint + `}`,
		"/api/v1/decisions/x": dec,
	})
	for _, args := range [][]string{{"decisions", "list"}, {"decisions", "show", "x"}} {
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
				t.Errorf("%v: a title forged the line %q", args, line)
			}
		}
	}
}

// A miss is reworded as for loops: an error with nothing on stdout, a hint
// that names the list command, and no address or token.
func TestCLIDecisionsShowOfAnUnknownNameFailsAndNamesTheListCommand(t *testing.T) {
	addr, _ := saServer(t, map[string]string{})
	out, err := saRun(t, addr, "decisions", "show", "nope")
	if err == nil {
		t.Fatal("decisions show nope must fail")
	}
	if out != "" {
		t.Errorf("decisions show nope printed %q on a miss", out)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want the status", err)
	}
	for _, bad := range []string{addr, strings.TrimPrefix(addr, "http://"), cliToken} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("error carries %q: %q", bad, err)
		}
	}
	plain := hostileServer(t, http.StatusNotFound, `{"error":{"code":"not_found","message":"no such decision","details":{"hint":"List the decisions (add status=all for the others), then use a name from the list."}}}`)
	_, err = saRun(t, plain, "decisions", "show", "nope")
	if err == nil || !strings.Contains(err.Error(), "goldie decisions list") {
		t.Errorf("error = %v, want the hint to name goldie decisions list", err)
	}
	other := hostileServer(t, http.StatusNotFound, `{"error":{"code":"not_found","message":"no such decision","details":{"hint":"Some other hint."}}}`)
	_, err = saRun(t, other, "decisions", "show", "nope")
	if err == nil || !strings.Contains(err.Error(), "Hint: Some other hint.") {
		t.Errorf("error = %v, want another server hint unchanged", err)
	}
}

// The exit codes are those of goldie loops, and wrong use makes no request.
func TestCLIDecisionsExitCodesAreThoseOfLoops(t *testing.T) {
	addr, rec := saServer(t, dcAnswers())
	run := func(args ...string) (string, string, int) {
		return runValidate(t, t.TempDir(), validateEnv(), append(args, "--addr", addr, "--token", cliToken)...)
	}
	for _, tc := range []struct {
		name         string
		loops, decis []string
	}{
		{"miss", []string{"loops", "show", "nope"}, []string{"decisions", "show", "nope"}},
		{"no verb", []string{"loops"}, []string{"decisions"}},
		{"unknown verb", []string{"loops", "frobnicate"}, []string{"decisions", "frobnicate"}},
		{"show without a name", []string{"loops", "show"}, []string{"decisions", "show"}},
		{"list with an argument", []string{"loops", "list", "extra"}, []string{"decisions", "list", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lo, _, lcode := run(tc.loops...)
			do, de, dcode := run(tc.decis...)
			if lcode == 0 || dcode != lcode {
				t.Errorf("goldie %v exit %d, want %d as goldie %v (stderr %q)", tc.decis, dcode, lcode, tc.loops, de)
			}
			if strings.Contains(de, "unknown command") {
				t.Errorf("goldie %v: stderr %q, want decisions to be a command", tc.decis, de)
			}
			if lo != "" || do != "" {
				t.Errorf("stdout loops %q decisions %q, want both empty", lo, do)
			}
		})
	}
	before := len(rec.requests())
	run("decisions")
	run("decisions", "show")
	run("decisions", "list", "extra")
	if n := len(rec.requests()); n != before {
		t.Errorf("wrong use made %d requests, want none", n-before)
	}
}

func TestRootHelpListsDecisionsAndItsHelpNamesListAndShow(t *testing.T) {
	stdout, _, code := runHelp(t, "--help")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if ask := section(t, stdout, "Ask a server", ""); !hasCommandLine(ask, "decisions") {
		t.Errorf("decisions is not listed with a description under Ask a server:\n%s", stdout)
	}
	out, stderr, code := runHelp(t, "decisions", "--help")
	wantCommandHelp(t, "decisions", out, stderr, code,
		[]string{"--addr", "--dir", "--token", "--json", "--project"}, []string{"process list", "list", "show"})
}

// An empty list says what to check next, as goldie loops list does.
func TestCLIDecisionsListOfNoDecisionsSaysWhatToCheckNext(t *testing.T) {
	addr, _ := saServer(t, map[string]string{"/api/v1/decisions": `{"decisions":[]}`})
	out, err := saRun(t, addr, "decisions", "list")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "empty decisions list", out, "No decisions", "goldie status")
}
