package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// --json escapes format characters instead of dropping them, so the value
// survives: a tag character, outside the Basic Multilingual Plane, must be
// written as a UTF-16 surrogate pair, or it decodes to a different string.
func TestCLIJSONEscapesFormatCharactersWithoutChangingTheValue(t *testing.T) {
	h := jsonString(t, hiddenCf)
	body := `{"id":"x","name":` + h + `,"keywords":[],"content":"c"}`
	out, err := runCLI(t, "lookup", "x", "--addr", hostileServer(t, http.StatusOK, body), "--token", cliToken, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if leaked := formatChars(out); len(leaked) > 0 {
		t.Errorf("--json output carries raw format characters %v:\n%q", leaked, out)
	}
	var got struct{ Name string }
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%q", err, out)
	}
	if got.Name != hiddenCf {
		t.Errorf("--json name decodes to %q, want %q", got.Name, hiddenCf)
	}
}
