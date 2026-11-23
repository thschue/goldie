package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #337: goldie version words a non-goldie answer and an
// error status as goldie status does, and a 401 whose body is not a valid
// error envelope reports a token state in both commands.
//
// The wording version uses, from status's phrases and wordError's text:
//
//	non-goldie answer  note "the server's answer isn't a goldie API answer"
//	                   hint "check the address (--addr or GOLDIE_ADDR)"
//	error status       note "<message> (<code> <text>)", or
//	                   "the server answered <code> <text>" without a message
//	                   hint: "Wait <n> seconds, then retry." for a valid
//	                   Retry-After, "Wait a little, then retry." for a 429
//	                   without one, else the server's cleaned hint, else none
//
// The note and hint fill version's existing shape, "server API: unknown
// (<note>)" and "Hint: <hint>"; --json keeps serverApiVersion (null),
// serverNote and hint. No existing golden or test pinned the old generic
// wording ("the server did not return its API document"), so none changes.

// answerServer answers every request with the given status, headers and body.
func answerServer(t *testing.T, code int, header map[string]string, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

var jsonType = map[string]string{"Content-Type": "application/json"}

// versionOut runs version in text and JSON mode against addr with a token and
// returns both outputs, failing if either exits non-zero.
func versionOut(t *testing.T, addr string) (text string, m map[string]any) {
	t.Helper()
	text, err := runCLI(t, "version", "--addr", addr, "--token", versionToken)
	if err != nil {
		t.Fatalf("version must exit 0: %v", err)
	}
	js, err := runCLI(t, "version", "--json", "--addr", addr, "--token", versionToken)
	if err != nil {
		t.Fatalf("version --json must exit 0: %v", err)
	}
	return text, versionJSON(t, js)
}

func requireNoLeak(t *testing.T, what, out, addr string, leaks ...string) {
	t.Helper()
	for _, leak := range append(leaks, mustHost(t, addr), versionToken) {
		if strings.Contains(out, leak) {
			t.Errorf("%s leaks %q: %q", what, leak, out)
		}
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func TestCLIVersionAgainstANonGoldieAnswerWordsItAsStatusDoes(t *testing.T) {
	const note = "the server's answer isn't a goldie API answer"
	const hint = "check the address (--addr or GOLDIE_ADDR)"
	cases := map[string]*httptest.Server{
		"html":         answerServer(t, 200, map[string]string{"Content-Type": "text/html"}, "<html>leak-body</html>"),
		"plain text":   answerServer(t, 200, map[string]string{"Content-Type": "text/plain"}, "leak-body"),
		"json no type": answerServer(t, 200, nil, `{"leak":"leak-body"}`),
	}
	for name, srv := range cases {
		t.Run(name, func(t *testing.T) {
			// status's words are the baseline version must share.
			_, serr := runCLI(t, "status", "--addr", srv.URL, "--token", versionToken)
			if serr == nil || !strings.Contains(serr.Error(), note) || !strings.Contains(serr.Error(), "check the address") {
				t.Fatalf("status = %v, want the non-goldie wording %q", serr, note)
			}
			text, m := versionOut(t, srv.URL)
			if want := "server API: unknown (" + note + ")"; !strings.Contains(text, want+"\n") {
				t.Errorf("version = %q, want the line %q", text, want)
			}
			if !strings.Contains(text, "Hint: "+hint) {
				t.Errorf("version = %q, want the hint %q", text, hint)
			}
			requireNullServerVersion(t, m)
			if m["serverNote"] != note || m["hint"] != hint {
				t.Errorf("version --json = %v, want serverNote %q and hint %q", m, note, hint)
			}
			if strings.Contains(text, "did not return") {
				t.Errorf("version = %q, must not use the generic wording", text)
			}
			requireNoLeak(t, "version", text, srv.URL, "leak-body")
		})
	}
}

// For an error status, version carries the status, the cleaned message and the
// hint, with the words status uses for the same error.
func TestCLIVersionAgainstAnErrorStatusCarriesTheStatusAndHint(t *testing.T) {
	retry := func(v string) map[string]string {
		return map[string]string{"Content-Type": "application/json", "Retry-After": v}
	}
	cases := []struct {
		name   string
		code   int
		header map[string]string
		body   string
		note   string // the version note
		hint   string // the version hint, "" for none
	}{
		{"403 with a hint", 403, jsonType,
			`{"error":{"code":"forbidden","message":"not allowed here","details":{"hint":"ask an operator"}}}`,
			"not allowed here (403 Forbidden)", "ask an operator"},
		{"429 with a wait", 429, retry("30"),
			`{"error":{"code":"rate_limited","message":"too many requests for this API token","details":{"hint":"Wait the number of seconds in the Retry-After header, then retry."}}}`,
			"too many requests for this API token (429 Too Many Requests)", "Wait 30 seconds, then retry."},
		{"429 with one second", 429, retry("1"),
			`{"error":{"code":"rate_limited","message":"slow down"}}`,
			"slow down (429 Too Many Requests)", "Wait 1 second, then retry."},
		{"429 without a wait", 429, jsonType,
			`{"error":{"code":"rate_limited","message":"slow down","details":{"hint":"Wait the number of seconds in the Retry-After header, then retry."}}}`,
			"slow down (429 Too Many Requests)", "Wait a little, then retry."},
		{"429 with a hostile wait", 429, retry("soon-marker"),
			`{"error":{"code":"rate_limited","message":"slow down"}}`,
			"slow down (429 Too Many Requests)", "Wait a little, then retry."},
		{"500 with a message", 500, jsonType,
			`{"error":{"code":"internal","message":"something broke"}}`,
			"something broke (500 Internal Server Error)", ""},
		{"500 without an envelope", 500, map[string]string{"Content-Type": "text/plain"},
			"panic: leak-body at /srv/leak-path/main.go:12",
			"the server answered 500 Internal Server Error", ""},
		{"500 with a truncated envelope", 500, jsonType,
			`{"error":{"code":"internal","message":"leak-body`,
			"the server answered 500 Internal Server Error", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := answerServer(t, c.code, c.header, c.body)
			text, m := versionOut(t, srv.URL)
			requireNullServerVersion(t, m)
			if want := "server API: unknown (" + c.note + ")"; !strings.Contains(text, want+"\n") {
				t.Errorf("version = %q, want the line %q", text, want)
			}
			if c.hint != "" && !strings.Contains(text, "Hint: "+c.hint+"\n") {
				t.Errorf("version = %q, want the hint %q", text, c.hint)
			}
			if c.hint == "" && strings.Contains(text, "Hint:") {
				t.Errorf("version = %q, want no hint", text)
			}
			if m["serverNote"] != c.note {
				t.Errorf("serverNote = %v, want %q", m["serverNote"], c.note)
			}
			if c.hint != "" && m["hint"] != c.hint {
				t.Errorf("hint = %v, want %q", m["hint"], c.hint)
			}
			if c.hint == "" && m["hint"] != nil {
				t.Errorf("hint = %v, want none", m["hint"])
			}
			requireNoLeak(t, "version", text, srv.URL, "leak-body", "leak-path", "soon-marker", "Wait 0 second")
			// status words the same error as its failure: same note, same hint.
			_, serr := runCLI(t, "status", "--addr", srv.URL, "--token", versionToken)
			if serr == nil {
				t.Fatal("status must fail on an error status")
			}
			want := c.note
			if c.hint != "" {
				want += "\nHint: " + c.hint
			}
			if c.name != "403 with a hint" && serr.Error() != want {
				t.Errorf("status = %q, want %q", serr.Error(), want)
			}
			if c.name == "403 with a hint" && !strings.HasPrefix(serr.Error(), c.note) {
				t.Errorf("status = %q, want it to begin with %q", serr.Error(), c.note)
			}
		})
	}
}

func TestCLIVersionAgainstAnErrorStatusStripsControlCharacters(t *testing.T) {
	srv := answerServer(t, 403, jsonType,
		`{"error":{"code":"forbidden","message":"bad\u001b[31mred‮name","details":{"hint":"fix\u001b[0m it"}}}`)
	text, m := versionOut(t, srv.URL)
	if strings.ContainsAny(text, "\x1b‮") {
		t.Errorf("version prints a control or bidi character: %q", text)
	}
	if !strings.Contains(text, "(403 Forbidden)") {
		t.Errorf("version = %q, want the status", text)
	}
	n, h := asString(m["serverNote"]), asString(m["hint"])
	if strings.ContainsAny(n+h, "\x1b‮") || !strings.Contains(n, "403") {
		t.Errorf("version --json note %q hint %q: want a clean note with the status", n, h)
	}
}

// A 401 whose body is not a valid error envelope is still a 401: the token
// state is reported, for both commands, with no part of the body echoed.
func TestCLIA401WithABadBodyReportsTheTokenState(t *testing.T) {
	big := contract.MaxResponseBytes + (1 << 20)
	bodies := map[string]string{
		"not json":       "leak-body: unauthorized",
		"truncated json": `{"error":{"code":"unauthorized","message":"leak-body`,
		"empty":          "",
		"html":           "<html>leak-body</html>",
		"oversized":      `{"error":{"message":"` + strings.Repeat("a", big) + `"}}`,
		"oversized junk": strings.Repeat("leak-body ", big/10),
	}
	for name, body := range bodies {
		srv := answerServer(t, 401, jsonType, body)
		t.Run(name, func(t *testing.T) {
			// No token given.
			out, err := runCLI(t, "version", "--addr", srv.URL)
			if err != nil {
				t.Fatalf("version without a token must exit 0: %v", err)
			}
			if !strings.Contains(out, "server API: unknown (no token given)") || strings.Contains(out, "rejected") {
				t.Errorf("version without a token = %q, want %q", out, "no token given")
			}
			js, err := runCLI(t, "version", "--json", "--addr", srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			m := versionJSON(t, js)
			requireNullServerVersion(t, m)
			if m["serverNote"] != "no token given" {
				t.Errorf("version --json without a token: serverNote = %v, want %q", m["serverNote"], "no token given")
			}
			st, err := runCLI(t, "status", "--addr", srv.URL)
			if err != nil {
				t.Fatalf("status without a token must exit 0: %v", err)
			}
			if !strings.Contains(st, "Auth: no token given") || strings.Contains(st, "rejected") {
				t.Errorf("status without a token = %q, want Auth: no token given", st)
			}
			requireNoLeak(t, "output", out+js+st, srv.URL, "leak-body")

			// A token given.
			out, err = runCLI(t, "version", "--addr", srv.URL, "--token", versionToken)
			if err != nil {
				t.Fatalf("version with a token must exit 0: %v", err)
			}
			if !strings.Contains(out, "server API: unknown (the token was rejected)") || strings.Contains(out, "no token") {
				t.Errorf("version with a token = %q, want %q", out, "the token was rejected")
			}
			js, err = runCLI(t, "version", "--json", "--addr", srv.URL, "--token", versionToken)
			if err != nil {
				t.Fatal(err)
			}
			m = versionJSON(t, js)
			requireNullServerVersion(t, m)
			if m["serverNote"] != "the token was rejected" {
				t.Errorf("version --json with a token: serverNote = %v, want %q", m["serverNote"], "the token was rejected")
			}
			st, err = runCLI(t, "status", "--addr", srv.URL, "--token", versionToken)
			if err != nil {
				t.Fatalf("status with a token must exit 0: %v", err)
			}
			if !strings.Contains(st, "Auth: token rejected") || strings.Contains(st, "no token") {
				t.Errorf("status with a token = %q, want Auth: token rejected", st)
			}
			requireNoLeak(t, "output", out+js+st, srv.URL, "leak-body")
		})
	}
}
