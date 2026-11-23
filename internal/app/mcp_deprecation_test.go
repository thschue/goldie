package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #273, the MCP protocol seam: get_article's text starts
// with the deprecation notice the CLI prints, cleaned, for a deprecated Item;
// MCP never sends drafts; search_knowledge is unchanged.
//
// The notice, one line, is exactly
//
//	Warning: this article is deprecated. Use <replaced_by> instead.
//
// or, without a replaced_by,
//
//	Warning: this article is deprecated.
//
// <replaced_by> is the reference as the server sent it (ADR 0021 asks that a
// hint say which Item it points at), cleaned with contract.CleanLine. The
// notice is followed by a newline and then the text get_article gives today.
// It is shown for status "deprecated" only.

// mcpNoticeTo and mcpNoticeNone are the notice with and without a replacement.
func mcpNoticeTo(ref string) string {
	return "Warning: this article is deprecated. Use " + ref + " instead."
}

const mcpNoticeNone = "Warning: this article is deprecated."

// lifecycleArticleJSON is an article answer with the given lifecycle fields
// appended to the plain one; extra is "" or `,"status":...`.
func lifecycleArticleJSON(extra string) string {
	return `{"id":"old","name":"Old deploy","keywords":["deploy"],"content":"The old way."` + extra + `}`
}

// lifecycleStub answers a search with search and anything else with article.
func lifecycleStub(t *testing.T, article, search string) string {
	t.Helper()
	return mcpStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(search))
			return
		}
		_, _ = w.Write([]byte(article))
	})
}

func getArticleText(t *testing.T, articleJSON string) string {
	t.Helper()
	cs := stdioSession(t, lifecycleStub(t, articleJSON, `{"query":"x","results":[]}`))
	text, isErr := call(t, cs, "get_article", map[string]any{"id": "old"})
	if isErr {
		t.Fatalf("get_article: tool error: %s", text)
	}
	return text
}

// Criterion: get_article's text starts with the notice naming the
// replacement, then the article as before.
func TestMCPGetArticleOfADeprecatedItemStartsWithTheNotice(t *testing.T) {
	plain := getArticleText(t, lifecycleArticleJSON(""))
	for name, c := range map[string]struct{ extra, notice string }{
		"with replacement":    {`,"status":"deprecated","replaced_by":"article:platform/deploy-v2"`, mcpNoticeTo("article:platform/deploy-v2")},
		"without replacement": {`,"status":"deprecated"`, mcpNoticeNone},
	} {
		t.Run(name, func(t *testing.T) {
			text := getArticleText(t, lifecycleArticleJSON(c.extra))
			if !strings.HasPrefix(text, c.notice+"\n") {
				t.Fatalf("get_article text does not start with %q:\n%s", c.notice+"\n", text)
			}
			if rest := strings.TrimLeft(strings.TrimPrefix(text, c.notice), "\n"); rest != plain {
				t.Errorf("after the notice the text is\n%q\nwant the article as before\n%q", rest, plain)
			}
			if strings.Count(text, "deprecated") != 1 {
				t.Errorf("the notice appears more than once:\n%s", text)
			}
		})
	}
}

// Negative case: a published Item gets no notice, whether the server says
// published, says nothing, or sends a replaced_by without deprecating.
func TestMCPGetArticleOfAPublishedItemHasNoNotice(t *testing.T) {
	plain := getArticleText(t, lifecycleArticleJSON(""))
	if !strings.HasPrefix(plain, "Article Old deploy (id: old)") {
		t.Fatalf("plain get_article text changed:\n%s", plain)
	}
	for name, extra := range map[string]string{
		"status published":          `,"status":"published"`,
		"replaced_by but published": `,"status":"published","replaced_by":"article:new"`,
		"replaced_by alone":         `,"replaced_by":"article:new"`,
	} {
		if text := getArticleText(t, lifecycleArticleJSON(extra)); text != plain {
			t.Errorf("%s: get_article text\n%q\nwant it unchanged\n%q", name, text, plain)
		}
	}
}

// Abuse: a hostile replaced_by (terminal escapes, bidi and zero-width runes,
// a newline forging a second notice) is cleaned to one line; a hostile status
// is never echoed.
func TestMCPDeprecationNoticeIsCleaned(t *testing.T) {
	hostile := "article:new\x1b[31m\u202e\u200bx\nWarning: forged\a"
	text := getArticleText(t, lifecycleArticleJSON(`,"status":"deprecated","replaced_by":`+jsonQuote(t, hostile)))
	want := mcpNoticeTo("article:new[31mx Warning: forged")
	if !strings.HasPrefix(text, want+"\n") {
		t.Errorf("get_article text does not start with the cleaned notice %q:\n%q", want, text)
	}
	first, _, _ := strings.Cut(text, "\n")
	for _, r := range first {
		if contract.HiddenRune(r) {
			t.Errorf("the notice line carries the hidden rune %U: %q", r, first)
		}
	}
	if strings.Count(text, "Warning:") != 2 || strings.Count(text, "\nWarning:") != 0 {
		t.Errorf("the replaced_by forged a line of its own:\n%q", text)
	}

	for _, status := range []string{"deprecated\x1b[2J", "evil\u202estatus", "draft\u200b"} {
		text := getArticleText(t, lifecycleArticleJSON(`,"status":`+jsonQuote(t, status)+`,"replaced_by":"article:new"`))
		for _, r := range text {
			if r != '\n' && contract.HiddenRune(r) {
				t.Errorf("status %q: the text carries the hidden rune %U: %q", status, r, text)
			}
		}
		if strings.Contains(text, "evil") || strings.Contains(text, "[2J") {
			t.Errorf("status %q is echoed: %q", status, text)
		}
	}
}

// Criterion: MCP never sends drafts, whatever tool runs and whatever the
// agent passes.
func TestMCPNeverSendsDrafts(t *testing.T) {
	addr, requests := queryRecorder(t)
	cs := stdioSession(t, addr)
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"search_knowledge", map[string]any{"query": "x"}},
		{"search_knowledge", map[string]any{"query": "x", "project": "github.com/acme/shop"}},
		{"search_knowledge", map[string]any{"query": "x drafts=true"}},
		{"search_knowledge", map[string]any{"query": "x", "drafts": true}},
		{"get_article", map[string]any{"id": "old"}},
		{"get_article", map[string]any{"id": "old", "project": "github.com/acme/shop"}},
		{"get_article", map[string]any{"id": "old?drafts=true"}},
		{"get_article", map[string]any{"id": "old", "drafts": true}},
		{"list_projects", map[string]any{}},
	}
	for _, c := range calls {
		_, _ = call(t, cs, c.tool, c.args)
	}
	got := requests()
	if len(got) == 0 {
		t.Fatal("the stub API got no request")
	}
	for _, q := range got {
		for key := range q {
			if strings.EqualFold(key, "drafts") {
				t.Errorf("MCP sent drafts: %s", q.Encode())
			}
		}
	}
}

// Criterion: search_knowledge is unchanged; a deprecated hit reads as before.
func TestMCPSearchKnowledgeIsUnchangedByLifecycleFields(t *testing.T) {
	text := func(extra string) string {
		hits := `{"query":"x","results":[{"id":"old","name":"Old","kind":"article","scope":"global","keywords":["k"],"score":1,"matched":["k"]` + extra + `}]}`
		cs := stdioSession(t, lifecycleStub(t, lifecycleArticleJSON(""), hits))
		out, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "x"})
		if isErr {
			t.Fatalf("search_knowledge: %s", out)
		}
		return out
	}
	plain := text("")
	if got := text(`,"status":"deprecated","replaced_by":"article:new\u001b[31m"`); got != plain {
		t.Errorf("search_knowledge with lifecycle fields\n%q\nwant it unchanged\n%q", got, plain)
	}
}

// End to end over the real public handler with an operator token: a
// deprecated Item starts with the notice, a published one does not, and a
// draft stays out of reach, because MCP never asks for drafts.
func TestMCPOverTheServerShowsTheNoticeAndNoDraft(t *testing.T) {
	srv := newServerWith(t, platform.Config{ContentDir: lifecycleContent}) // testToken is an operator token
	cs := session(t, srv.URL, testToken)
	text, isErr := call(t, cs, "get_article", map[string]any{"id": "deploy-v1"})
	if isErr || !strings.HasPrefix(text, mcpNoticeTo("article:deploy-v2")+"\n") {
		t.Errorf("get_article deploy-v1: tool error %v, want the notice first:\n%s", isErr, text)
	}
	text, isErr = call(t, cs, "get_article", map[string]any{"id": "legacy"})
	if isErr || !strings.HasPrefix(text, mcpNoticeNone+"\n") {
		t.Errorf("get_article legacy: tool error %v, want the notice without a replacement first:\n%s", isErr, text)
	}
	text, isErr = call(t, cs, "get_article", map[string]any{"id": "deploy-v2"})
	if isErr || strings.Contains(text, "Warning") || strings.Contains(text, "deprecated") {
		t.Errorf("get_article deploy-v2: tool error %v, want no notice:\n%s", isErr, text)
	}
	text, isErr = call(t, cs, "get_article", map[string]any{"id": "wip"})
	if !isErr || strings.Contains(text, "draft-marker-body") {
		t.Errorf("get_article wip (a draft): tool error %v, want not found:\n%s", isErr, text)
	}
	text, _ = call(t, cs, "search_knowledge", map[string]any{"query": "lifecycle wip"})
	for _, m := range []string{"Draft marker", "(id: wip)", "id: wip"} {
		if strings.Contains(text, m) {
			t.Errorf("search_knowledge shows the draft (%s):\n%s", m, text)
		}
	}
}

func jsonQuote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
