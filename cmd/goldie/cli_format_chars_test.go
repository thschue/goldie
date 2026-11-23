package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"
)

// Acceptance tests for #128: CLI output drops Unicode format characters
// (category Cf: zero-width characters, the byte-order mark, tag characters),
// as MCP output does. A person reading the Source does not see them, but an
// agent reading the CLI output does.

// hiddenCf hides one format character between each visible letter: zero-width
// space, non-joiner and joiner, word joiner, byte-order mark, soft hyphen,
// Mongolian vowel separator, interlinear annotation anchor, the language tag
// and two tag letters. Visible, it reads "abcdefghijk".
const hiddenCf = "a\u200bb\u200cc\u200dd\u2060e\ufefff\u00adg\u180eh\ufff9i\U000E0001j\U000E0049\U000E0047k"

// formatChars lists the Cf runes in s, so a failure names what leaked.
func formatChars(s string) []string {
	var found []string
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			found = append(found, fmt.Sprintf("%U", r))
		}
	}
	return found
}

func hostileServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCLILookupDropsFormatCharactersFromServerText(t *testing.T) {
	h := jsonString(t, hiddenCf)
	body := `{"id":` + h + `,"name":` + h + `,"keywords":[` + h + `],"content":` +
		jsonString(t, "first "+hiddenCf+"\nsecond "+hiddenCf) + `}`
	out, err := runCLI(t, "lookup", "x", "--addr", hostileServer(t, http.StatusOK, body), "--token", cliToken)
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(out); len(leaked) > 0 {
		t.Errorf("lookup output carries format characters %v:\n%q", leaked, out)
	}
	want := "abcdefghijk\nid: abcdefghijk\nkeywords: abcdefghijk\n\nfirst abcdefghijk\nsecond abcdefghijk\n"
	if !strings.HasPrefix(out, want) {
		t.Errorf("lookup output = %q, want it to start with %q", out, want)
	}
}

func TestCLISearchDropsFormatCharactersFromServerText(t *testing.T) {
	h := jsonString(t, hiddenCf)
	body := `{"query":"q","results":[{"id":` + h + `,"name":` + h + `,"score":3,"keywords":[` + h + `],"matched":[` + h + `]}]}`
	out, err := runCLI(t, "search", "q", "--addr", hostileServer(t, http.StatusOK, body), "--token", cliToken)
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(out); len(leaked) > 0 {
		t.Errorf("search output carries format characters %v:\n%q", leaked, out)
	}
	for _, want := range []string{"  abcdefghijk  abcdefghijk\n", "matched: abcdefghijk\n", "keywords: abcdefghijk\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("search output = %q, want it to contain %q", out, want)
		}
	}
}

// The query the user typed is echoed back; format characters in it are
// dropped too, so the echo shows what the user can see.
func TestCLISearchEchoDropsFormatCharactersFromTheQuery(t *testing.T) {
	out, err := runCLI(t, "search", hiddenCf, "--addr", hostileServer(t, http.StatusOK, `{"query":"q","results":[]}`), "--token", cliToken)
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(out); len(leaked) > 0 {
		t.Errorf("search output carries format characters %v:\n%q", leaked, out)
	}
	if !strings.Contains(out, `"abcdefghijk"`) {
		t.Errorf("search output = %q, want the query echoed as %q", out, "abcdefghijk")
	}
}

func TestCLIErrorDropsFormatCharactersFromTheEnvelope(t *testing.T) {
	h := jsonString(t, hiddenCf)
	body := `{"error":{"code":"not_found","message":` + h + `,"details":{"hint":` + h + `}}}`
	_, err := runCLI(t, "lookup", "x", "--addr", hostileServer(t, http.StatusNotFound, body), "--token", cliToken)
	if err == nil {
		t.Fatal("expected an error")
	}
	if leaked := formatChars(err.Error()); len(leaked) > 0 {
		t.Errorf("error carries format characters %v:\n%q", leaked, err)
	}
	if want := "abcdefghijk (404 Not Found)\nHint: " + lookupHint; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// A code the CLI does not reword keeps the server's hint, and the hint is
// cleaned of format characters.
func TestCLIErrorDropsFormatCharactersFromAKeptHint(t *testing.T) {
	h := jsonString(t, hiddenCf)
	body := `{"error":{"code":"bad_request","message":` + h + `,"details":{"hint":` + h + `}}}`
	_, err := runCLI(t, "lookup", "x", "--addr", hostileServer(t, http.StatusBadRequest, body), "--token", cliToken)
	if err == nil {
		t.Fatal("expected an error")
	}
	if leaked := formatChars(err.Error()); len(leaked) > 0 {
		t.Errorf("error carries format characters %v:\n%q", leaked, err)
	}
	if want := "abcdefghijk (400 Bad Request)\nHint: abcdefghijk"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestCLIStatusDropsFormatCharactersFromTheAPIDocument(t *testing.T) {
	h := jsonString(t, hiddenCf)
	out, err := runCLI(t, "status", "--addr", hostileServer(t, http.StatusOK, `{"info":{"title":`+h+`,"version":`+h+`}}`))
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(out); len(leaked) > 0 {
		t.Errorf("status output carries format characters %v:\n%q", leaked, out)
	}
	if !strings.Contains(out, "API: abcdefghijk abcdefghijk\n") {
		t.Errorf("status output = %q, want the API line without format characters", out)
	}
}

func TestCLIVersionDropsFormatCharactersFromTheServerAPIVersion(t *testing.T) {
	h := jsonString(t, hiddenCf)
	out, err := runCLI(t, "version", "--addr", hostileServer(t, http.StatusOK, `{"info":{"title":"t","version":`+h+`}}`))
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(out); len(leaked) > 0 {
		t.Errorf("version output carries format characters %v:\n%q", leaked, out)
	}
	if !strings.Contains(out, "server API: abcdefghijk\n") {
		t.Errorf("version output = %q, want the server API version without format characters", out)
	}
}

// --json keeps values (controls are escaped as \uXXXX, not dropped, see
// TestCLISanitisesLinesAndJSON), so a format character may survive only in
// escaped form: never as a raw rune in the output.
func TestCLIJSONCarriesNoRawFormatCharacters(t *testing.T) {
	h := jsonString(t, hiddenCf)
	body := `{"id":` + h + `,"name":` + h + `,"keywords":[` + h + `],"content":` + h + `}`
	out, err := runCLI(t, "lookup", "x", "--addr", hostileServer(t, http.StatusOK, body), "--token", cliToken, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(out); len(leaked) > 0 {
		t.Errorf("--json output carries raw format characters %v:\n%q", leaked, out)
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("--json output is not valid JSON: %q", out)
	}
}

// Negative case: visible text, including letters outside ASCII, combining
// marks, emoji with skin-tone modifiers and markup, is not a format
// character and is printed unchanged.
func TestCLIKeepsVisibleUnicodeAndMarkup(t *testing.T) {
	const visible = "Grüße 日本語 e\u0301 👍🏽 <script>`rm -rf`</script> <!-- x -->"
	v := jsonString(t, visible)
	body := `{"id":"x","name":` + v + `,"keywords":[],"content":` + v + `}`
	out, err := runCLI(t, "lookup", "x", "--addr", hostileServer(t, http.StatusOK, body), "--token", cliToken)
	if err != nil {
		t.Fatal(err)
	}
	if want := visible + "\nid: x\nkeywords: (none)\n\n" + visible + "\n"; !strings.HasPrefix(out, want) {
		t.Errorf("lookup output = %q, want it to start with %q", out, want)
	}
}

// The wording of the CLI's errors stays the CLI's own after the decoder is
// shared: an answer without an envelope names only the status, and none of
// the body.
func TestCLIErrorWithoutAnEnvelopeNamesOnlyTheStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>http://10.0.0.1/internal</html>"))
	}))
	defer srv.Close()
	_, err := runCLI(t, "lookup", "x", "--addr", srv.URL, "--token", cliToken)
	if err == nil {
		t.Fatal("expected an error")
	}
	if want := "the server answered 502 Bad Gateway"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}
