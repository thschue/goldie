package app

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

// Acceptance tests for #734 (slice of #34): search accepts kind=decision, over
// the HTTP contract through the public handler. The pattern followed is the
// skill and agent cases of skill_file_search_kind_test.go, on the decisions
// fixture of decisions_api_test.go.
//
// The contract they fix, beyond the issue:
//   - a decision hit has id = the decision's name (its file stem), name = its
//     title (as an article's name is its display title), kind=decision and the
//     Scope that won (global, or the Project);
//   - decisions match on title and keywords like other Items; without kind=
//     search still returns articles only;
//   - only accepted decisions are hits; a superseded decision is replaced by
//     its accepted successor, however long the chain.

// searchDecisionEdits adds to decisionsFixture: marmot (accepted), marmot-new
// (proposed), marmot-no (rejected) and marmot-old (deprecated), all with the
// keyword marmot; and quokka-1, quokka-2 and quokka-3, a chain whose first two
// are superseded and whose last is accepted and carries no quokka of its own.
func searchDecisionEdits() map[string]string {
	return map[string]string{
		"global/decisions/marmot.md":     decisionFile("Marmot cache", "accepted", "2023-01-01", "keywords: [marmot]"),
		"global/decisions/marmot-new.md": decisionFile("Marmot proposal", "proposed", "2023-01-02", "keywords: [marmot]"),
		"global/decisions/marmot-no.md":  decisionFile("Marmot refusal", "rejected", "2023-01-03", "keywords: [marmot]"),
		"global/decisions/marmot-old.md": decisionFile("Marmot legacy", "deprecated", "2023-01-04", "keywords: [marmot]"),
		"global/decisions/quokka-1.md":   decisionFile("Quokka store", "superseded", "2021-01-01", "keywords: [quokka]", "superseded_by: decision:quokka-2"),
		"global/decisions/quokka-2.md":   decisionFile("Quokka store v2", "superseded", "2021-06-01", "keywords: [quokka]", "supersedes: [decision:quokka-1]", "superseded_by: decision:quokka-3"),
		"global/decisions/quokka-3.md":   decisionFile("Wombat store", "accepted", "2022-01-01", "supersedes: [decision:quokka-2]"),
	}
}

func decisionSearchHits(t *testing.T, query string) []searchHitEntry {
	t.Helper()
	srv, _ := decisionServer(t, searchDecisionEdits())
	return searchHits(t, srv, readerToken, query)
}

// Criterion 1: the served spec's kind enum gains decision.
func TestServedSpecSearchKindEnumHasDecision(t *testing.T) {
	paths, _ := specDoc(t)["paths"].(map[string]any)
	search, _ := paths["/api/v1/search"].(map[string]any)["get"].(map[string]any)
	params, _ := search["parameters"].([]any)
	for _, p := range params {
		m, _ := p.(map[string]any)
		if m["name"] != "kind" {
			continue
		}
		schema, _ := m["schema"].(map[string]any)
		enum, _ := schema["enum"].([]any)
		var got []string
		for _, e := range enum {
			s, _ := e.(string)
			got = append(got, s)
		}
		for _, want := range []string{"article", "skill", "agent", "decision"} {
			if !slices.Contains(got, want) {
				t.Errorf("the kind enum is %v, want it to hold %s (additive)", got, want)
			}
		}
		return
	}
	t.Error("the search operation declares no kind query parameter")
}

// Criterion 2: by default only accepted decisions match.
func TestSearchDecisionsReturnsOnlyAcceptedOnes(t *testing.T) {
	hits := decisionSearchHits(t, "q=marmot&kind=decision")
	if got := hitIDs(hits); !slices.Equal(got, []string{"marmot"}) {
		t.Errorf("q=marmot&kind=decision hits %v, want [marmot]: proposed, rejected and deprecated never appear", got)
	}
	// The title alone is enough: the word "refusal" is in the rejected title only.
	if got := hitIDs(decisionSearchHits(t, "q=refusal&kind=decision")); len(got) != 0 {
		t.Errorf("q=refusal&kind=decision hits %v, want none: the match is a rejected decision", got)
	}
	// A superseded decision is not a hit under its own id.
	for _, h := range decisionSearchHits(t, "q=quokka&kind=decision") {
		if h.ID == "quokka-1" || h.ID == "quokka-2" {
			t.Errorf("a superseded decision %s is a hit", h.ID)
		}
	}
}

// Criterion 3: a hit carries kind=decision, the name, the title and the Scope.
func TestSearchDecisionHitCarriesKindNameTitleAndScope(t *testing.T) {
	srv, _ := decisionServer(t, searchDecisionEdits())
	hits := searchHits(t, srv, readerToken, "q=queue&kind=decision")
	if len(hits) != 1 {
		t.Fatalf("q=queue&kind=decision hits %v, want [alpha]", hitIDs(hits))
	}
	if h := hits[0]; h.ID != "alpha" || h.Name != "Alpha" || h.Kind != "decision" || h.Scope != "global" {
		t.Errorf("hit = %+v, want id alpha, name (title) Alpha, kind decision, scope global", h)
	}
	if !slices.Contains(hits[0].Matched, "queue") {
		t.Errorf("matched = %v, want the keyword queue", hits[0].Matched)
	}
	// A title word matches too.
	if got := hitIDs(searchHits(t, srv, readerToken, "q=cache&kind=decision")); !slices.Equal(got, []string{"marmot"}) {
		t.Errorf("q=cache (a title word) hits %v, want [marmot]", got)
	}
	// In a Project's view the Project's decision answers with its Scope.
	hits = searchHits(t, srv, readerToken, "q=shop&kind=decision&project="+decisionsShop)
	if len(hits) == 0 {
		t.Fatal("q=shop in the shop Project's view found no decision")
	}
	for _, h := range hits {
		if h.Kind != "decision" || h.Scope != decisionsShop {
			t.Errorf("Project hit = %+v, want kind decision and scope %s", h, decisionsShop)
		}
	}
	// Without kind= search returns articles only (ADR 0011).
	for _, h := range searchHits(t, srv, readerToken, "q=marmot") {
		t.Errorf("q=marmot without kind= returned %+v, want no decision", h)
	}
}

// Criterion 4: searching a superseded decision's title returns its accepted
// successor, not the old record.
func TestSearchSupersededDecisionReturnsAcceptedSuccessor(t *testing.T) {
	for _, q := range []string{"quokka", "Quokka+store"} {
		hits := decisionSearchHits(t, "q="+q+"&kind=decision")
		if len(hits) != 1 {
			t.Fatalf("q=%s hits %v, want the successor [quokka-3] only", q, hitIDs(hits))
		}
		if h := hits[0]; h.ID != "quokka-3" || h.Name != "Wombat store" || h.Kind != "decision" || h.Scope != "global" {
			t.Errorf("q=%s hit = %+v, want the accepted successor quokka-3 (Wombat store)", q, h)
		}
	}
	// A query that matches the successor and the old records gives one hit.
	if got := hitIDs(decisionSearchHits(t, "q=store&kind=decision")); !slices.Equal(got, []string{"quokka-3"}) {
		t.Errorf("q=store hits %v, want [quokka-3] once", got)
	}
}

// Criterion 5: an unknown kind is still the 400 envelope with a hint, which
// names decision among the kinds and does not echo the input.
func TestSearchUnknownKindIsStill400WithHint(t *testing.T) {
	srv, _ := decisionServer(t, nil)
	for _, kind := range []string{"decisions", "Decision", "loop", "%3Cscript%3Ealert(1)%3C%2Fscript%3E"} {
		status, body := itemsGet(t, srv, readerToken, "/api/v1/search?q=alpha&kind="+kind)
		if status != http.StatusBadRequest {
			t.Errorf("kind=%s: status %d, want 400: %s", kind, status, body)
			continue
		}
		code, hint := errorHint(t, body)
		if code != "bad_request" {
			t.Errorf("kind=%s: code %q, want bad_request", kind, code)
		}
		if !strings.Contains(hint, "decision") {
			t.Errorf("kind=%s: hint %q does not name kind=decision", kind, hint)
		}
		if strings.Contains(string(body), "script") {
			t.Errorf("kind=%s: the answer echoes the input: %s", kind, body)
		}
	}
}

// A superseded_by reference is read as everywhere else: the name matches
// ignoring case (ADR 0021).
func TestSearchSupersededByMixedCaseReferenceReachesSuccessor(t *testing.T) {
	edits := searchDecisionEdits()
	edits["global/decisions/quokka-1.md"] = decisionFile("Quokka store", "superseded", "2021-01-01", "keywords: [quokka]", "superseded_by: decision:Quokka-2")
	srv, _ := decisionServer(t, edits)
	if got := hitIDs(searchHits(t, srv, readerToken, "q=quokka&kind=decision")); !slices.Equal(got, []string{"quokka-3"}) {
		t.Errorf("hits %v, want [quokka-3]", got)
	}
}

// A hit's id resolves to an accepted decision: a Project's non-accepted copy of
// a global accepted name does not hide the global one from search.
func TestSearchDecisionHitSurvivesProjectNonAcceptedCopy(t *testing.T) {
	edits := searchDecisionEdits()
	edits["projects/"+decisionsShop+"/decisions/alpha.md"] = decisionFile("Shop Alpha", "proposed", "2023-07-02")
	srv, _ := decisionServer(t, edits)
	hits := searchHits(t, srv, readerToken, "q=queue&kind=decision&project="+decisionsShop)
	if len(hits) != 1 || hits[0].ID != "alpha" || hits[0].Scope != "global" {
		t.Errorf("hits %+v, want the global accepted alpha", hits)
	}
}

// With drafts allowed every status is served as stored, a superseded record
// under its own id.
func TestSearchDecisionsWithDraftsServesEveryStatusAsStored(t *testing.T) {
	srv, _ := decisionServer(t, searchDecisionEdits())
	got := hitIDs(searchHits(t, srv, operatorToken, "q=marmot&kind=decision&drafts=true"))
	slices.Sort(got)
	if want := []string{"marmot", "marmot-new", "marmot-no", "marmot-old"}; !slices.Equal(got, want) {
		t.Errorf("drafts q=marmot hits %v, want %v", got, want)
	}
	got = hitIDs(searchHits(t, srv, operatorToken, "q=quokka&kind=decision&drafts=true"))
	slices.Sort(got)
	if want := []string{"quokka-1", "quokka-2"}; !slices.Equal(got, want) {
		t.Errorf("drafts q=quokka hits %v, want %v", got, want)
	}
}
