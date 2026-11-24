package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// Acceptance tests for #714 (slice of #34): list and get decisions over the
// HTTP contract, through the public handler. The pattern followed is
// loops_api_test.go (the server, the spec check in itemsGet, errorHint).
//
// The contract they fix, beyond the issue:
//   - GET /api/v1/decisions answers {"decisions": [...], "hint"?}, one entry
//     per name (the nearest Scope wins), sorted by name; GET
//     /api/v1/decisions/{name} answers one decision with content and chain;
//   - status=<value> lists the decisions of exactly that status, all lists
//     every status; the default is accepted. It is open to readers;
//   - a get serves a decision of any status (a superseded decision has a
//     history to show), and the chain reaches decisions of any status;
//   - supersedes is a list of references as written in the file
//     ("decision:<name>"); superseded_by is such a reference, empty or
//     absent when the decision is not superseded;
//   - the chain is the decision's engine chain (see decision_chain_test.go in
//     the engine): oldest to newest, including the decision itself;
//   - the hint, as for every other read, is the unregistered-Project hint.

const (
	decisionsAPI  = "/api/v1/decisions"
	decisionsShop = "github.com/acme/shop"
)

type decisionChainEntry struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Scope  string `json:"scope"`
}

type decisionEntry struct {
	Name         string               `json:"name"`
	Title        string               `json:"title"`
	Status       string               `json:"status"`
	Date         string               `json:"date"`
	Scope        string               `json:"scope"`
	Supersedes   []string             `json:"supersedes"`
	SupersededBy string               `json:"superseded_by"`
	Keywords     []string             `json:"keywords"`
	Content      string               `json:"content"`
	Chain        []decisionChainEntry `json:"chain"`
	Hint         string               `json:"hint"`
}

func decisionFile(title, status, date string, front ...string) string {
	return "---\ntitle: " + title + "\nstatus: " + status + "\ndate: " + date + "\n" +
		strings.Join(front, "\n") + "\n---\nBody of " + title + ".\n"
}

// decisionsFixture is the base content: global decisions alpha (accepted,
// keywords db and queue), zeta (accepted), beta (proposed), gamma (rejected),
// delta (deprecated) and the chain old-a, old-b, old-c (the first two
// superseded); and the Project github.com/acme/shop with its own accepted
// shop-pick and an accepted alpha that shadows the global one.
func decisionsFixture() map[string]string {
	return map[string]string{
		"global/decisions/zeta.md":  decisionFile("Zeta", "accepted", "2023-04-01"),
		"global/decisions/alpha.md": decisionFile("Alpha", "accepted", "2023-05-02", "keywords: [db, queue]"),
		"global/decisions/beta.md":  decisionFile("Beta", "proposed", "2023-06-01"),
		"global/decisions/gamma.md": decisionFile("Gamma", "rejected", "2023-06-02"),
		"global/decisions/delta.md": decisionFile("Delta", "deprecated", "2023-06-03"),
		"global/decisions/old-a.md": decisionFile("Old A", "superseded", "2022-01-01", "superseded_by: decision:old-b"),
		"global/decisions/old-b.md": decisionFile("Old B", "superseded", "2022-02-01", "superseded_by: decision:old-c", "supersedes: [decision:old-a]"),
		"global/decisions/old-c.md": decisionFile("Old C", "accepted", "2022-03-01", "supersedes: [decision:old-b]"),

		"projects/" + decisionsShop + "/decisions/shop-pick.md": decisionFile("Shop Pick", "accepted", "2023-07-01"),
		"projects/" + decisionsShop + "/decisions/alpha.md":     decisionFile("Shop Alpha", "accepted", "2023-07-02"),
	}
}

func decisionServer(t *testing.T, edits map[string]string) (*httptest.Server, string) {
	t.Helper()
	files := decisionsFixture()
	for k, v := range edits {
		files[k] = v
	}
	dir := t.TempDir()
	for name, content := range files {
		writeRaw(t, dir, name, content)
	}
	return loopServer(t, dir), dir
}

func listDecisions(t *testing.T, srv *httptest.Server, token, path string) ([]decisionEntry, string) {
	t.Helper()
	status, body := itemsGet(t, srv, token, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var out struct {
		Decisions *[]decisionEntry `json:"decisions"`
		Hint      string           `json:"hint"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Decisions == nil {
		t.Fatalf("GET %s: no decisions list (%v): %s", path, err, body)
	}
	return *out.Decisions, out.Hint
}

func getDecision(t *testing.T, srv *httptest.Server, token, path string) decisionEntry {
	t.Helper()
	status, body := itemsGet(t, srv, token, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200: %s", path, status, body)
	}
	var e decisionEntry
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, body)
	}
	return e
}

func decisionNameList(ds []decisionEntry) []string {
	names := []string{}
	for _, d := range ds {
		names = append(names, d.Name)
	}
	return names
}

func chainNameList(c []decisionChainEntry) []string {
	names := []string{}
	for _, e := range c {
		names = append(names, e.Name)
	}
	return names
}

// Criterion: the served spec declares the two operations.
func TestServedSpecDeclaresDecisionOperations(t *testing.T) {
	ops := specOperations(t)
	for _, want := range []string{"GET /api/v1/decisions", "GET /api/v1/decisions/{name}"} {
		if !slices.Contains(ops, want) {
			t.Errorf("the spec declares no %s; it declares %v", want, ops)
		}
	}
}

// Criterion: the list returns only accepted decisions, sorted by name, each
// with name, title, status, date, scope, supersedes, superseded_by, keywords.
func TestListDecisionsDefaultsToAcceptedSortedByNameWithItsFields(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	got, hint := listDecisions(t, srv, readerToken, decisionsAPI)
	if names := decisionNameList(got); !slices.Equal(names, []string{"alpha", "old-c", "zeta"}) {
		t.Fatalf("default list = %v, want [alpha old-c zeta]: only accepted, sorted, no Project decision", names)
	}
	if hint != "" {
		t.Errorf("a global read carries the hint %q", hint)
	}
	alpha, oldC := got[0], got[1]
	if alpha.Title != "Alpha" || alpha.Status != "accepted" || alpha.Date != "2023-05-02" || alpha.Scope != "global" ||
		!slices.Equal(alpha.Keywords, []string{"db", "queue"}) || len(alpha.Supersedes) != 0 || alpha.SupersededBy != "" {
		t.Errorf("alpha = %+v", alpha)
	}
	if oldC.Date != "2022-03-01" || !slices.Equal(oldC.Supersedes, []string{"decision:old-b"}) {
		t.Errorf("old-c = %+v, want date 2022-03-01 and supersedes [decision:old-b]", oldC)
	}
	// An empty list is [] not null, as a client iterates it.
	_, body := itemsGet(t, srv, readerToken, decisionsAPI+"?status=rejected&project="+decisionsShop)
	if strings.Contains(string(body), `"decisions":null`) {
		t.Errorf("an empty list is null: %s", body)
	}
}

// Criterion: status=... returns the others; open to readers, and not the
// operator-gated drafts parameter.
func TestListDecisionsStatusFilterReturnsTheOtherStatuses(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	for status, want := range map[string][]string{
		"accepted":   {"alpha", "old-c", "zeta"},
		"proposed":   {"beta"},
		"rejected":   {"gamma"},
		"deprecated": {"delta"},
		"superseded": {"old-a", "old-b"},
		"all":        {"alpha", "beta", "delta", "gamma", "old-a", "old-b", "old-c", "zeta"},
	} {
		got, _ := listDecisions(t, srv, readerToken, decisionsAPI+"?status="+status)
		if names := decisionNameList(got); !slices.Equal(names, want) {
			t.Errorf("status=%s lists %v, want %v", status, names, want)
		}
	}
	got, _ := listDecisions(t, srv, readerToken, decisionsAPI+"?status=superseded")
	if len(got) != 2 || got[0].SupersededBy != "decision:old-b" || got[1].SupersededBy != "decision:old-c" {
		t.Errorf("superseded list = %+v, want superseded_by decision:old-b and decision:old-c", got)
	}
	// drafts is not the control here (status= is): whatever the API does with
	// it, it must never hand a reader the non-accepted decisions.
	if status, body := itemsGet(t, srv, readerToken, decisionsAPI+"?drafts=true"); status == http.StatusOK {
		for _, hidden := range []string{"beta", "gamma", "delta", "old-a", "old-b"} {
			if strings.Contains(string(body), `"name":"`+hidden+`"`) {
				t.Errorf("drafts=true leaked the non-accepted decision %s: %s", hidden, body)
			}
		}
	}
}

// Criterion: an unknown status is a 400 envelope that does not echo it.
func TestListDecisionsRejectsAnUnknownStatusWithoutEchoingIt(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	for _, v := range []string{"SECRETVALUE", "ACCEPTED", "accepted,proposed", "draft", "%3Cscript%3E", "all%00"} {
		status, body := itemsGet(t, srv, readerToken, decisionsAPI+"?status="+v)
		if status != http.StatusBadRequest {
			t.Errorf("status=%q: status %d, want 400: %s", v, status, body)
			continue
		}
		assertEnvelope(t, body, "")
		for _, leak := range []string{"SECRETVALUE", "script", "ACCEPTED"} {
			if strings.Contains(v, leak) && strings.Contains(string(body), leak) {
				t.Errorf("status=%q: the 400 echoes %q: %s", v, leak, body)
			}
		}
	}
}

// Criterion: the get returns the decision with its raw content and the chain,
// oldest to newest, each entry {name, title, status, scope}. Given A to B to C,
// asking for B returns A, B, C.
func TestGetDecisionCarriesContentAndTheChainOldestToNewest(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	got := getDecision(t, srv, readerToken, decisionsAPI+"/old-b")
	if got.Name != "old-b" || got.Title != "Old B" || got.Status != "superseded" || got.Date != "2022-02-01" ||
		got.Scope != "global" || got.SupersededBy != "decision:old-c" || !slices.Equal(got.Supersedes, []string{"decision:old-a"}) {
		t.Errorf("get old-b = %+v", got)
	}
	if got.Content != "Body of Old B.\n" {
		t.Errorf("content = %q, want the raw body", got.Content)
	}
	want := []decisionChainEntry{
		{"old-a", "Old A", "superseded", "global"},
		{"old-b", "Old B", "superseded", "global"},
		{"old-c", "Old C", "accepted", "global"},
	}
	if !slices.Equal(got.Chain, want) {
		t.Errorf("chain = %+v, want %+v", got.Chain, want)
	}
	// The chain entry holds exactly the four keys: no body, no file, no Source.
	_, body := itemsGet(t, srv, readerToken, decisionsAPI+"/old-b")
	var raw struct {
		Chain []map[string]json.RawMessage `json:"chain"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || len(raw.Chain) != 3 || len(raw.Chain[0]) != 4 {
		t.Errorf("chain entries %+v (%v), want objects of name, title, status and scope only", raw.Chain, err)
	}
	// A decision with no history has a chain of itself.
	if got := getDecision(t, srv, readerToken, decisionsAPI+"/zeta"); !slices.Equal(chainNameList(got.Chain), []string{"zeta"}) {
		t.Errorf("zeta chain = %+v, want only itself", got.Chain)
	}
}

// Criterion: two predecessors of the same successor are ordered by date, then name.
func TestGetDecisionOrdersTwoPredecessorsByDateThenName(t *testing.T) {
	srv, _ := decisionServer(t, map[string]string{
		"global/decisions/p-late.md": decisionFile("P Late", "superseded", "2023-06-01", "superseded_by: decision:merged"),
		"global/decisions/p-b.md":    decisionFile("P B", "superseded", "2023-05-01", "superseded_by: decision:merged"),
		"global/decisions/p-a.md":    decisionFile("P A", "superseded", "2023-05-01", "superseded_by: decision:merged"),
		"global/decisions/merged.md": decisionFile("Merged", "accepted", "2023-07-01",
			"supersedes: [decision:p-late, decision:p-b, decision:p-a]"),
	})
	got := getDecision(t, srv, readerToken, decisionsAPI+"/merged")
	if names := chainNameList(got.Chain); !slices.Equal(names, []string{"p-a", "p-b", "p-late", "merged"}) {
		t.Errorf("chain = %v, want [p-a p-b p-late merged]", names)
	}
}

// Criterion: a Project view answers with the nearest copy, and the scope says which.
func TestDecisionsResolveInTheProjectView(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	q := "?project=" + decisionsShop
	got, hint := listDecisions(t, srv, readerToken, decisionsAPI+q)
	if names := decisionNameList(got); !slices.Equal(names, []string{"alpha", "old-c", "shop-pick", "zeta"}) {
		t.Fatalf("shop view lists %v", names)
	}
	if hint != "" {
		t.Errorf("a registered Project carries the hint %q", hint)
	}
	if got[0].Title != "Shop Alpha" || got[0].Scope != decisionsShop || got[3].Scope != "global" {
		t.Errorf("shop view: alpha %+v, zeta scope %q; want the Project's alpha and the global zeta", got[0], got[3].Scope)
	}
	if one := getDecision(t, srv, readerToken, decisionsAPI+"/alpha"+q); one.Title != "Shop Alpha" || one.Scope != decisionsShop {
		t.Errorf("get alpha in the shop view = %+v", one)
	}
	if status, body := itemsGet(t, srv, readerToken, decisionsAPI+"/shop-pick"); status != http.StatusNotFound {
		t.Errorf("a Project decision in the global view: status %d, want 404: %s", status, body)
	}
}

// Criterion: an unregistered Project gets the global answer with a hint.
func TestDecisionsForAnUnregisteredProjectAreGlobalWithAHint(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	const id = "github.com/acme/none"
	got, hint := listDecisions(t, srv, readerToken, decisionsAPI+"?project="+id)
	if names := decisionNameList(got); !slices.Equal(names, []string{"alpha", "old-c", "zeta"}) || got[0].Scope != "global" {
		t.Errorf("list = %v, want the global answer", names)
	}
	if hint != wantUnregisteredHint(id) {
		t.Errorf("list hint = %q, want the unregistered hint", hint)
	}
	one := getDecision(t, srv, readerToken, decisionsAPI+"/old-b?project="+id)
	if one.Hint != wantUnregisteredHint(id) || one.Scope != "global" || len(one.Chain) != 3 {
		t.Errorf("get = %+v, want the global answer with the unregistered hint", one)
	}
}

// Criterion: a miss is a 404 envelope with a hint (ADR 0014), and a project
// that is not a ProjectId is a 400. The token is required.
func TestDecisionMissesBadInputAndTheToken(t *testing.T) {
	srv, dir := decisionServer(t, nil)
	status, body := itemsGet(t, srv, readerToken, decisionsAPI+"/nope-SECRETNAME")
	if status != http.StatusNotFound {
		t.Fatalf("a missing decision: status %d, want 404: %s", status, body)
	}
	if code, hint := errorHint(t, body); code != "not_found" || hint == "" || strings.Contains(string(body), "SECRETNAME") {
		t.Errorf("a missing decision: code %q hint %q body %s, want not_found with a hint that echoes nothing", code, hint, body)
	}
	for _, p := range []string{decisionsAPI + "?project=..%2Fetc", decisionsAPI + "/alpha?project=..%2Fetc", decisionsAPI + "?project=not-a-project"} {
		if status, body := itemsGet(t, srv, readerToken, p); status != http.StatusBadRequest {
			t.Errorf("GET %s: status %d, want 400: %s", p, status, body)
		}
	}
	for _, p := range []string{decisionsAPI, decisionsAPI + "/alpha"} {
		if status, body := itemsGet(t, srv, "", p); status != http.StatusUnauthorized {
			t.Errorf("GET %s without a token: status %d, want 401: %s", p, status, body)
		}
	}
	// No path in an error, and no Source or file path in an answer.
	_, listBody := itemsGet(t, srv, readerToken, decisionsAPI+"?status=all")
	_, getBody := itemsGet(t, srv, readerToken, decisionsAPI+"/old-b")
	for _, b := range [][]byte{body, listBody, getBody} {
		if strings.Contains(string(b), dir) || strings.Contains(string(b), "global/decisions") || strings.Contains(string(b), ".md") {
			t.Errorf("a response names a path or file: %s", b)
		}
	}
}

// Criterion: the change is snake_case (ADR 0024).
func TestDecisionPayloadIsSnakeCase(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	_, body := itemsGet(t, srv, readerToken, decisionsAPI+"/old-b")
	if !strings.Contains(string(body), `"superseded_by"`) || strings.Contains(string(body), "supersededBy") {
		t.Errorf("the payload does not use superseded_by: %s", body)
	}
}

// #757 criterion 1: for a Project whose view holds a non-accepted copy of a
// decision, the list and the get answer the same record.
func TestDecisionListAndGetAgreeOnAProjectNonAcceptedCopy(t *testing.T) {
	srv, _ := decisionServer(t, map[string]string{
		"projects/" + decisionsShop + "/decisions/alpha.md": decisionFile("Shop Alpha", "proposed", "2023-07-02"),
	})
	q := "?project=" + decisionsShop
	list, _ := listDecisions(t, srv, readerToken, decisionsAPI+q)
	var listed *decisionEntry
	for i := range list {
		if list[i].Name == "alpha" {
			listed = &list[i]
		}
	}
	status, body := itemsGet(t, srv, readerToken, decisionsAPI+"/alpha"+q)
	if listed == nil {
		if status != http.StatusNotFound {
			t.Fatalf("the list omits alpha but GET answers %d: %s", status, body)
		}
		return
	}
	if status != http.StatusOK {
		t.Fatalf("the list holds alpha but GET answers %d: %s", status, body)
	}
	got := getDecision(t, srv, readerToken, decisionsAPI+"/alpha"+q)
	if got.Title != listed.Title || got.Status != listed.Status || got.Scope != listed.Scope || got.Date != listed.Date {
		t.Errorf("GET alpha = %q/%s/%s/%s, the list holds %q/%s/%s/%s; want the same record",
			got.Title, got.Status, got.Scope, got.Date, listed.Title, listed.Status, listed.Scope, listed.Date)
	}
}
