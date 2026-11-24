package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// #449: GET /api/v1/sync names each Source of GOLDIE_SOURCES with its
// preference and the Items it shadows, and POST /api/v1/sync?source=<name>
// triggers that Source only. The Sources poll once an hour, so only a POST can
// make a new commit show.

type shadowedBy struct {
	Source  string  `json:"source"`
	Scope   string  `json:"scope"`
	Project *string `json:"project"`
}

type shadowedItem struct {
	Kind       string     `json:"kind"`
	ID         string     `json:"id"`
	Scope      string     `json:"scope"`
	Project    *string    `json:"project"`
	ShadowedBy shadowedBy `json:"shadowed_by"`
}

type namedSourceStatus struct {
	Name       string         `json:"name"`
	Preference int            `json:"preference"`
	Shadowed   []shadowedItem `json:"shadowed"`
}

// twoSourceServer serves the git Sources alpha then beta, in that preference
// order, with the named reader and operator tokens, once both have loaded.
func twoSourceServer(t *testing.T, a, b *bareRepo) *runningGit {
	t.Helper()
	cfg := sourcesConfig(t, time.Hour, nil, gitSpec("alpha", a.url), gitSpec("beta", b.url))
	cfg.APIToken = ""
	cfg.APITokens = namedTokenLines()
	g := startGit(t, cfg)
	readyOn(t, g, "source:alpha", "source:beta")
	return g
}

func sourceStatuses(t *testing.T, g *runningGit) []namedSourceStatus {
	t.Helper()
	resp, body := send(t, g.pub, http.MethodGet, statusPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", statusPath, resp.StatusCode, body)
	}
	var got struct {
		Sources []namedSourceStatus `json:"sources"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	return got.Sources
}

func servedToOperator(t *testing.T, g *runningGit, id string) bool {
	t.Helper()
	resp, _ := send(t, g.pub, http.MethodGet, "/api/v1/articles/"+id, bearer(operatorToken))
	return resp.StatusCode == http.StatusOK
}

// Each Source of the list is named as configured, in preference order (1 =
// first listed), and the later one lists the Item the earlier one shadows.
func TestSyncStatusNamesEachSourceWithItsPreferenceAndShadowedItems(t *testing.T) {
	a, b := newBareRepo(t), newBareRepo(t)
	a.commit(with(gitArticle("style"), gitArticle("only-a")))
	b.commit(with(gitArticle("style"), gitArticle("only-b")))
	g := twoSourceServer(t, a, b)

	got := sourceStatuses(t, g)
	if len(got) != 2 {
		t.Fatalf("sources = %+v, want alpha and beta", got)
	}
	if got[0].Name != "alpha" || got[0].Preference != 1 || got[1].Name != "beta" || got[1].Preference != 2 {
		t.Errorf("sources = %+v, want alpha preference 1 then beta preference 2", got)
	}
	if len(got[0].Shadowed) != 0 {
		t.Errorf("alpha shadowed = %+v, want none: it wins", got[0].Shadowed)
	}
	if len(got[1].Shadowed) != 1 {
		t.Fatalf("beta shadowed = %+v, want exactly article style", got[1].Shadowed)
	}
	s := got[1].Shadowed[0]
	if s.Kind != "article" || s.ID != "style" || s.Scope != "global" || s.Project != nil {
		t.Errorf("shadowed item = %+v, want article style in the global scope", s)
	}
	if s.ShadowedBy.Source != "alpha" || s.ShadowedBy.Scope != "global" || s.ShadowedBy.Project != nil {
		t.Errorf("shadowed_by = %+v, want alpha in the global scope", s.ShadowedBy)
	}
}

// POST ?source=beta polls beta only: its new commit is served at once, and
// alpha's, which nothing triggered, is not.
func TestPostSyncWithASourceTriggersThatSourceOnly(t *testing.T) {
	a, b := newBareRepo(t), newBareRepo(t)
	a.commit(gitArticle("a-first"))
	b.commit(gitArticle("b-first"))
	g := twoSourceServer(t, a, b)
	a.commit(with(gitArticle("a-first"), gitArticle("a-new")))
	b.commit(with(gitArticle("b-first"), gitArticle("b-new")))

	resp, body := send(t, g.pub, http.MethodPost, statusPath+"?source=beta", bearer(operatorToken))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s?source=beta = %d, want 202: %s", statusPath, resp.StatusCode, body)
	}
	eventually(t, 10*time.Second, "beta's new commit was never served", func() bool { return servedToOperator(t, g, "b-new") })
	time.Sleep(300 * time.Millisecond)
	if servedToOperator(t, g, "a-new") {
		t.Error("alpha's new commit is served: the trigger for beta polled alpha too")
	}
}

// Without source, every Source of the list is triggered and the answer is
// 202, not an empty status and 503 not_ready.
func TestPostSyncWithoutASourceTriggersEverySourceOfTheList(t *testing.T) {
	a, b := newBareRepo(t), newBareRepo(t)
	a.commit(gitArticle("a-first"))
	b.commit(gitArticle("b-first"))
	g := twoSourceServer(t, a, b)
	a.commit(with(gitArticle("a-first"), gitArticle("a-new")))
	b.commit(with(gitArticle("b-first"), gitArticle("b-new")))

	resp, body := send(t, g.pub, http.MethodPost, statusPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST %s = %d, want 202: %s", statusPath, resp.StatusCode, body)
	}
	for _, id := range []string{"a-new", "b-new"} {
		eventually(t, 10*time.Second, id+" was never served", func() bool { return servedToOperator(t, g, id) })
	}
}

// An unknown Source name is a 404 in the envelope with a fixed hint, echoes
// nothing of the input, and triggers no poll.
func TestPostSyncWithAnUnknownSourceAnswers404WithoutEchoingTheName(t *testing.T) {
	a, b := newBareRepo(t), newBareRepo(t)
	a.commit(gitArticle("a-first"))
	b.commit(gitArticle("b-first"))
	g := twoSourceServer(t, a, b)
	a.commit(with(gitArticle("a-first"), gitArticle("a-new")))

	const hostile = "<script>nope-9f3a</script>"
	resp, body := send(t, g.pub, http.MethodPost, statusPath+"?source="+url.QueryEscape(hostile), bearer(operatorToken))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.StatusCode, body)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	const hint = "read GET /api/v1/sync for the configured Source names"
	if env.Error.Code != "not_found" || env.Error.Message != "no such Source" || len(env.Error.Details) != 1 || env.Error.Details["hint"] != hint {
		t.Errorf("error = %+v, want not_found, %q and the hint %q alone", env.Error, "no such Source", hint)
	}
	if strings.Contains(string(body), "nope-9f3a") || strings.Contains(string(body), "script") {
		t.Errorf("the 404 echoes the Source name: %s", body)
	}
	time.Sleep(300 * time.Millisecond)
	if servedToOperator(t, g, "a-new") {
		t.Error("a refused trigger polled a Source")
	}
}

// The shadowed list of a Source is capped: 101 Items hidden by alpha show as
// exactly 100 (maxShadowed in internal/api).
func TestSyncStatusCapsTheShadowedListAtOneHundred(t *testing.T) {
	const items = 101
	files := map[string]string{}
	for i := range items {
		for k, v := range gitArticle(fmt.Sprintf("dup-%03d", i)) {
			files[k] = v
		}
	}
	a, b := newBareRepo(t), newBareRepo(t)
	a.commit(files)
	b.commit(files)
	g := twoSourceServer(t, a, b)

	got := sourceStatuses(t, g)
	if len(got) != 2 {
		t.Fatalf("sources = %d, want alpha and beta", len(got))
	}
	if n := len(got[1].Shadowed); n != 100 {
		t.Errorf("beta shadowed = %d Items, want the cap of 100 of the %d it has hidden", n, items)
	}
}

// A server with one Source, not a list, still names it and gives it
// preference 1, and refuses an unknown source name with 404.
func TestSyncStatusOfASingleSourceNamesItAndRefusesAnUnknownName(t *testing.T) {
	r := newBareRepo(t)
	r.commit(gitArticle("only"))
	cfg := gitConfig(t, r.url, time.Hour)
	cfg.GitAllowFileURL = true
	g := startGit(t, cfg)
	eventually(t, 5*time.Second, "the Source never loaded", func() bool {
		code, _, _ := readyz(t, g.admin)
		return code == http.StatusOK
	})

	resp, body := send(t, g.pub, http.MethodGet, statusPath, bearer(testToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", statusPath, resp.StatusCode, body)
	}
	var st struct {
		Sources []namedSourceStatus `json:"sources"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Sources) != 1 || st.Sources[0].Name != "git" || st.Sources[0].Preference != 1 {
		t.Errorf("sources = %+v, want one named git with preference 1", st.Sources)
	}

	resp, body = send(t, g.pub, http.MethodPost, statusPath+"?source=nope", bearer(testToken))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST ?source=nope = %d, want 404: %s", resp.StatusCode, body)
	}
}

// Triggering one Source stays operator-only: a reader is refused and nothing
// polls.
func TestPostSyncWithASourceRefusesAReaderWithoutPolling(t *testing.T) {
	a, b := newBareRepo(t), newBareRepo(t)
	a.commit(gitArticle("a-first"))
	b.commit(gitArticle("b-first"))
	g := twoSourceServer(t, a, b)
	b.commit(with(gitArticle("b-first"), gitArticle("b-new")))

	resp, body := send(t, g.pub, http.MethodPost, statusPath+"?source=beta", bearer(readerToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("reader: status %d, want 403: %s", resp.StatusCode, body)
	}
	time.Sleep(300 * time.Millisecond)
	if servedToOperator(t, g, "b-new") {
		t.Error("a reader's trigger polled beta")
	}
}
