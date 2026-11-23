package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Acceptance tests for #409: goldie skills list|show <name> and goldie agents
// list|show <name>, with text and --json output. The CLI never resolves
// anything itself, so the real-server cases run against the example content
// (a deploy skill, a reviewer agent), and the stub cases pin what the CLI asks
// for and how it words hostile server text. Pattern: lookup and search in
// cli_test.go and cli_format_chars_test.go.
//
// show --file is #538 and is not tested here.

// saRecorder is a stub API that records each request and answers per path.
type saRecorder struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (r *saRecorder) requests() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*http.Request(nil), r.reqs...)
}

// saServer answers each exact URL path in answers with its JSON body and 200,
// and every other path with a not_found envelope and 404.
func saServer(t *testing.T, answers map[string]string) (string, *saRecorder) {
	t.Helper()
	rec := &saRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, r.Clone(r.Context()))
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if body, ok := answers[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such item","details":{"hint":"List them first."}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, rec
}

const (
	saSkillList = `{"skills":[` +
		`{"name":"deploy","files":[{"path":"SKILL.md","size":16},{"path":"scripts/run.sh","size":18}]},` +
		`{"name":"release","files":[{"path":"SKILL.md","size":9}]}]}`
	saSkillDeploy = `{"name":"deploy","files":[{"path":"SKILL.md","size":16},{"path":"scripts/run.sh","size":18}]}`
	saAgentList   = `{"agents":[{"name":"planner","description":"Plans the work"},{"name":"reviewer","description":"Reviews code"}]}`
	saAgentShow   = `{"name":"reviewer","description":"Reviews code"}`
)

func saAnswers() map[string]string {
	return map[string]string{
		"/api/v1/skills":          saSkillList,
		"/api/v1/skills/deploy":   saSkillDeploy,
		"/api/v1/agents":          saAgentList,
		"/api/v1/agents/reviewer": saAgentShow,
	}
}

func saRun(t *testing.T, addr string, args ...string) (string, error) {
	t.Helper()
	return runCLI(t, append(args, "--addr", addr, "--token", cliToken)...)
}

func requireContains(t *testing.T, what, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("%s = %q, want it to contain %q", what, out, w)
		}
	}
}

// inOrder reports whether each want appears in out after the previous one.
func inOrder(out string, wants ...string) bool {
	at := 0
	for _, w := range wants {
		i := strings.Index(out[at:], w)
		if i < 0 {
			return false
		}
		at += i + len(w)
	}
	return true
}

func TestCLISkillsListShowsEverySkillWithItsFiles(t *testing.T) {
	addr, rec := saServer(t, saAnswers())
	out, err := saRun(t, addr, "skills", "list")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "skills list", out, "deploy", "release", "SKILL.md", "scripts/run.sh")
	if !inOrder(out, "deploy", "release") {
		t.Errorf("skills list = %q, want the skills in the order the server sent them (sorted by name)", out)
	}
	if reqs := rec.requests(); len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].URL.Path != "/api/v1/skills" {
		t.Errorf("skills list made requests %v, want one GET /api/v1/skills", reqs)
	}
}

func TestCLISkillsListJSONIsTheAPIAnswer(t *testing.T) {
	addr, _ := saServer(t, saAnswers())
	out, err := saRun(t, addr, "skills", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Skills []struct {
			Name  string
			Files []struct {
				Path string
				Size int
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("skills list --json is not JSON: %v\n%q", err, out)
	}
	if len(got.Skills) != 2 || got.Skills[0].Name != "deploy" || got.Skills[1].Name != "release" {
		t.Fatalf("skills = %+v, want deploy then release", got.Skills)
	}
	if f := got.Skills[0].Files; len(f) != 2 || f[1].Path != "scripts/run.sh" || f[1].Size != 18 {
		t.Errorf("deploy files = %+v, want SKILL.md and scripts/run.sh (18 bytes)", f)
	}
}

func TestCLISkillsListOfNoSkillsIsNotAnError(t *testing.T) {
	addr, _ := saServer(t, map[string]string{"/api/v1/skills": `{"skills":[]}`})
	out, err := saRun(t, addr, "skills", "list")
	if err != nil {
		t.Fatalf("an empty list must not be an error: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("skills list printed nothing for no skills, want a line that says there are none")
	}
	jsonOut, err := saRun(t, addr, "skills", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Skills []any }
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil || got.Skills == nil || len(got.Skills) != 0 {
		t.Errorf("skills list --json = %q (%v), want an empty skills array", jsonOut, err)
	}
}

func TestCLISkillsShowPrintsTheSkillAndItsFiles(t *testing.T) {
	addr, rec := saServer(t, saAnswers())
	out, err := saRun(t, addr, "skills", "show", "deploy")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "skills show", out, "deploy", "SKILL.md", "scripts/run.sh")
	if strings.Contains(out, "release") {
		t.Errorf("skills show deploy = %q, must show only that skill", out)
	}
	if reqs := rec.requests(); len(reqs) != 1 || reqs[0].URL.Path != "/api/v1/skills/deploy" {
		t.Errorf("skills show made requests %v, want one GET /api/v1/skills/deploy", reqs)
	}
}

func TestCLISkillsShowJSONIsTheAPIAnswer(t *testing.T) {
	addr, _ := saServer(t, saAnswers())
	out, err := saRun(t, addr, "skills", "show", "deploy", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Name  string
		Files []struct {
			Path string
			Size int
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("skills show --json is not JSON: %v\n%q", err, out)
	}
	if got.Name != "deploy" || len(got.Files) != 2 || got.Files[0].Path != "SKILL.md" || got.Files[0].Size != 16 {
		t.Errorf("skills show --json = %+v, want deploy with SKILL.md (16 bytes) and scripts/run.sh", got)
	}
}

func TestCLIAgentsListShowsEveryAgentWithItsDescription(t *testing.T) {
	addr, rec := saServer(t, saAnswers())
	out, err := saRun(t, addr, "agents", "list")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "agents list", out, "planner", "Plans the work", "reviewer", "Reviews code")
	if !inOrder(out, "planner", "reviewer") {
		t.Errorf("agents list = %q, want the agents in the order the server sent them (sorted by name)", out)
	}
	if reqs := rec.requests(); len(reqs) != 1 || reqs[0].URL.Path != "/api/v1/agents" {
		t.Errorf("agents list made requests %v, want one GET /api/v1/agents", reqs)
	}
}

func TestCLIAgentsListJSONIsTheAPIAnswer(t *testing.T) {
	addr, _ := saServer(t, saAnswers())
	out, err := saRun(t, addr, "agents", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Agents []struct{ Name, Description string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("agents list --json is not JSON: %v\n%q", err, out)
	}
	if len(got.Agents) != 2 || got.Agents[1].Name != "reviewer" || got.Agents[1].Description != "Reviews code" {
		t.Errorf("agents = %+v, want planner and reviewer with its description", got.Agents)
	}
}

func TestCLIAgentsListOfNoAgentsIsNotAnError(t *testing.T) {
	addr, _ := saServer(t, map[string]string{"/api/v1/agents": `{"agents":[]}`})
	out, err := saRun(t, addr, "agents", "list")
	if err != nil {
		t.Fatalf("an empty list must not be an error: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("agents list printed nothing for no agents, want a line that says there are none")
	}
}

func TestCLIAgentsShowPrintsTheAgentAndItsDescription(t *testing.T) {
	addr, rec := saServer(t, saAnswers())
	out, err := saRun(t, addr, "agents", "show", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	requireContains(t, "agents show", out, "reviewer", "Reviews code")
	if reqs := rec.requests(); len(reqs) != 1 || reqs[0].URL.Path != "/api/v1/agents/reviewer" {
		t.Errorf("agents show made requests %v, want one GET /api/v1/agents/reviewer", reqs)
	}
	jsonOut, err := saRun(t, addr, "agents", "show", "reviewer", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Name, Description string }
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("agents show --json is not JSON: %v\n%q", err, jsonOut)
	}
	if got.Name != "reviewer" || got.Description != "Reviews code" {
		t.Errorf("agents show --json = %+v, want reviewer, Reviews code", got)
	}
}

// The commands against a real server over the example content, so the CLI and
// the API agree on the shapes.
func TestCLISkillsAndAgentsReadTheExampleContent(t *testing.T) {
	srv := cliServer(t)
	cases := []struct {
		args  []string
		wants []string
	}{
		{[]string{"skills", "list"}, []string{"deploy", "SKILL.md"}},
		{[]string{"skills", "show", "deploy"}, []string{"deploy", "SKILL.md"}},
		{[]string{"agents", "list"}, []string{"reviewer", "Reviews code globally"}},
		{[]string{"agents", "show", "reviewer"}, []string{"reviewer", "Reviews code globally"}},
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

// Local mode: --dir starts an embedded server over the content, as for search
// and lookup, so no token and no address are needed.
func TestCLISkillsAndAgentsReadLocalContentWithDir(t *testing.T) {
	dir := dirContent(t)
	for args, want := range map[string]string{"skills list": "deploy", "agents show reviewer": "Reviews code globally"} {
		out, err := runCLI(t, append(strings.Fields(args), "--dir", dir)...)
		if err != nil {
			t.Fatalf("%s --dir: %v", args, err)
		}
		requireContains(t, args+" --dir", out, want)
	}
}

func TestCLISkillsAndAgentsAskInTheProject(t *testing.T) {
	addr, rec := saServer(t, saAnswers())
	const project = "github.com/acme/shop"
	for _, args := range [][]string{
		{"skills", "list"}, {"skills", "show", "deploy"}, {"agents", "list"}, {"agents", "show", "reviewer"},
	} {
		if _, err := saRun(t, addr, append(args, "--project", project)...); err != nil {
			t.Fatalf("%v --project: %v", args, err)
		}
	}
	reqs := rec.requests()
	if len(reqs) != 4 {
		t.Fatalf("made %d requests, want 4", len(reqs))
	}
	for _, r := range reqs {
		if got := r.URL.Query().Get("project"); got != project {
			t.Errorf("%s asked with project=%q, want %q", r.URL.Path, got, project)
		}
	}
}

func TestCLISkillsAndAgentsSendTheTokenAsBearer(t *testing.T) {
	addr, rec := saServer(t, saAnswers())
	if _, err := saRun(t, addr, "skills", "list"); err != nil {
		t.Fatal(err)
	}
	if _, err := saRun(t, addr, "agents", "show", "reviewer"); err != nil {
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

func TestCLISkillsAndAgentsWithoutATokenFailBeforeAnyRequest(t *testing.T) {
	addr, rec := saServer(t, saAnswers())
	for _, args := range [][]string{{"skills", "list"}, {"skills", "show", "deploy"}, {"agents", "list"}, {"agents", "show", "reviewer"}} {
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

// A miss is an error that prints nothing on stdout, and never echoes the
// address or token.
func TestCLISkillsAndAgentsShowOfAnUnknownNameFails(t *testing.T) {
	addr, _ := saServer(t, saAnswers())
	for _, args := range [][]string{{"skills", "show", "nope"}, {"agents", "show", "nope"}} {
		out, err := saRun(t, addr, args...)
		if err == nil {
			t.Fatalf("%v must fail", args)
		}
		if out != "" {
			t.Errorf("%v printed %q on a miss", args, out)
		}
		if !strings.Contains(err.Error(), "404") {
			t.Errorf("%v error = %q, want the status", args, err)
		}
		for _, bad := range []string{addr, strings.TrimPrefix(addr, "http://"), cliToken} {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("%v error carries %q: %q", args, bad, err)
			}
		}
	}
}

func TestCLISkillsAndAgentsWithABadTokenFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad token","details":{}}}`))
	}))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{{"skills", "list"}, {"agents", "list"}, {"skills", "show", "x"}, {"agents", "show", "x"}} {
		out, err := saRun(t, srv.URL, args...)
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("%v: err = %v, want the 401", args, err)
		}
		if out != "" {
			t.Errorf("%v printed %q", args, out)
		}
	}
}

// Wrong use is an error that makes no request.
func TestCLISkillsAndAgentsMisuseMakesNoRequest(t *testing.T) {
	addr, rec := saServer(t, saAnswers())
	cases := [][]string{
		{"skills"},
		{"agents"},
		{"skills", "frobnicate"},
		{"agents", "frobnicate"},
		{"skills", "show"},
		{"agents", "show"},
		{"skills", "show", "a", "b"},
		{"agents", "show", "a", "b"},
		{"skills", "list", "extra"},
		{"agents", "list", "extra"},
	}
	for _, args := range cases {
		out, err := saRun(t, addr, args...)
		if err == nil {
			t.Errorf("%v must fail", args)
		}
		if strings.Contains(out, "deploy") || strings.Contains(out, "reviewer") {
			t.Errorf("%v printed content: %q", args, out)
		}
	}
	if n := len(rec.requests()); n != 0 {
		t.Errorf("made %d requests for wrong use, want none", n)
	}
	// Control: right use of the same commands asks the server.
	if _, err := saRun(t, addr, "skills", "list"); err != nil {
		t.Fatalf("skills list: %v", err)
	}
	if _, err := saRun(t, addr, "agents", "show", "reviewer"); err != nil {
		t.Fatalf("agents show reviewer: %v", err)
	}
	if n := len(rec.requests()); n != 2 {
		t.Errorf("right use made %d requests, want 2", n)
	}
}

// A name that would be resolved by the URL path into another route is refused
// before any request, as lookup does; one with a slash stays one path segment.
func TestCLISkillsAndAgentsShowKeepsTheNameOneSegment(t *testing.T) {
	// Control: a plain name is asked for, so the refusals below are the
	// command's, not an absent command's.
	ctlAddr, ctl := saServer(t, saAnswers())
	if _, err := saRun(t, ctlAddr, "skills", "show", "deploy"); err != nil {
		t.Fatalf("skills show deploy: %v", err)
	}
	if n := len(ctl.requests()); n != 1 {
		t.Fatalf("skills show deploy made %d requests, want 1", n)
	}
	for _, kind := range []string{"skills", "agents"} {
		for _, name := range []string{"", ".", ".."} {
			addr, rec := saServer(t, saAnswers())
			if _, err := saRun(t, addr, kind, "show", name); err == nil {
				t.Errorf("%s show %q must fail", kind, name)
			}
			if n := len(rec.requests()); n != 0 {
				t.Errorf("%s show %q made %d requests, want none", kind, name, n)
			}
		}
		for _, name := range []string{"a/b", "../agents/reviewer", "x?project=evil", "x#frag", "a%2Fb"} {
			addr, rec := saServer(t, saAnswers())
			_, _ = saRun(t, addr, kind, "show", name)
			for _, r := range rec.requests() {
				if want := "/api/v1/" + kind + "/" + url.PathEscape(name); r.URL.EscapedPath() != want {
					t.Errorf("%s show %q requested %q, want %q", kind, name, r.URL.EscapedPath(), want)
				}
				if r.URL.RawQuery != "" {
					t.Errorf("%s show %q sent the query %q", kind, name, r.URL.RawQuery)
				}
			}
		}
	}
}

// Server text is untrusted: format characters and terminal escapes are cleaned
// in the text output (CleanText), and escaped without changing the value in
// --json.
func TestCLISkillsAndAgentsDropFormatCharactersAndEscapesFromServerText(t *testing.T) {
	h := jsonString(t, hiddenCf)
	esc := jsonString(t, "x\x1b[2Jy\u202ez\u2028w")
	hint := `"hint":` + h
	addr, _ := saServer(t, map[string]string{
		"/api/v1/skills":        `{"skills":[{"name":` + h + `,"files":[{"path":` + h + `,"size":1}]}],` + hint + `}`,
		"/api/v1/skills/deploy": `{"name":` + h + `,"files":[{"path":` + h + `,"size":1}],` + hint + `}`,
		"/api/v1/agents":        `{"agents":[{"name":` + h + `,"description":` + jsonString(t, "d "+hiddenCf+"\nnext "+hiddenCf) + `}],` + hint + `}`,
		"/api/v1/agents/x":      `{"name":` + h + `,"description":` + esc + `,` + hint + `}`,
	})
	for _, args := range [][]string{{"skills", "list"}, {"skills", "show", "deploy"}, {"agents", "list"}, {"agents", "show", "x"}} {
		out, err := saRun(t, addr, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if leaked := formatChars(out); len(leaked) > 0 {
			t.Errorf("%v output carries format characters %v:\n%q", args, leaked, out)
		}
		if strings.Contains(out, "\x1b") {
			t.Errorf("%v output carries a terminal escape:\n%q", args, out)
		}
		requireContains(t, strings.Join(args, " "), out, "abcdefghijk")
	}

	jsonOut, err := saRun(t, addr, "skills", "show", "deploy", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(jsonOut); len(leaked) > 0 {
		t.Errorf("skills show --json carries raw format characters %v:\n%q", leaked, jsonOut)
	}
	var got struct{ Name string }
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil || got.Name != hiddenCf {
		t.Errorf("skills show --json name = %q (%v), want the value unchanged", got.Name, err)
	}
}

// A skill's file path or a name cannot forge a line or an entry in the text
// output: it is one line.
func TestCLISkillsListKeepsServerNamesOnOneLine(t *testing.T) {
	forged := jsonString(t, "deploy\nFORGED: injected line")
	addr, _ := saServer(t, map[string]string{
		"/api/v1/skills": `{"skills":[{"name":` + forged + `,"files":[{"path":` + forged + `,"size":1}]}]}`,
	})
	out, err := saRun(t, addr, "skills", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "FORGED") {
			t.Errorf("skills list lets server text start a line of its own: %q", out)
		}
	}
}

func TestCLIAnswerThatIsNotAnAPIAnswerIsRefusedForSkillsAndAgents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>Sign in leak-marker</html>"))
	}))
	t.Cleanup(srv.Close)
	for _, args := range [][]string{{"skills", "list"}, {"agents", "list"}, {"skills", "show", "deploy"}, {"agents", "show", "reviewer"}} {
		out, err := saRun(t, srv.URL, args...)
		if err == nil {
			t.Errorf("%v must fail for an answer that is not an API answer", args)
			continue
		}
		if out != "" || strings.Contains(err.Error(), "leak-marker") || strings.Contains(err.Error(), srv.URL) {
			t.Errorf("%v out = %q, err = %q: must show neither the page nor the address", args, out, err)
		}
	}
}

// The server's hint for an unregistered Project reaches the user, as for
// search, in text and in --json.
func TestCLISkillsAndAgentsListPassOnTheServersHint(t *testing.T) {
	const hint = "The Project github.com/acme/none is not registered on this server, so this is global guidance only."
	addr, _ := saServer(t, map[string]string{
		"/api/v1/skills": `{"skills":[],"hint":"` + hint + `"}`,
		"/api/v1/agents": `{"agents":[],"hint":"` + hint + `"}`,
	})
	for _, args := range [][]string{{"skills", "list"}, {"agents", "list"}} {
		out, err := saRun(t, addr, append(args, "--project", "github.com/acme/none")...)
		if err != nil {
			t.Fatal(err)
		}
		requireContains(t, strings.Join(args, " "), out, "Hint: "+hint)
		jsonOut, err := saRun(t, addr, append(args, "--project", "github.com/acme/none", "--json")...)
		if err != nil {
			t.Fatal(err)
		}
		requireContains(t, strings.Join(args, " ")+" --json", jsonOut, hint)
	}
}

func TestRootHelpListsSkillsAndAgents(t *testing.T) {
	stdout, _, code := runHelp(t, "--help")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	ask := section(t, stdout, "Ask a server", "")
	for _, c := range []string{"skills", "agents"} {
		if !hasCommandLine(ask, c) {
			t.Errorf("%q is not listed with a description under Ask a server:\n%s", c, stdout)
		}
	}
	for _, c := range []string{"skills", "agents"} {
		out, stderr, code := runHelp(t, c, "--help")
		if code != 0 || stderr != "" {
			t.Errorf("goldie %s --help: exit %d, stderr %q", c, code, stderr)
		}
		requireContains(t, "goldie "+c+" --help", out, "list", "show")
	}
}
