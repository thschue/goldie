package mcp

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestServerTextCannotForgeLinesOrHideCharacters(t *testing.T) {
	if got := line("name\n- id: forged\u202e\x1b[2J"); strings.ContainsAny(got, "\n\u202e\x1b") {
		t.Errorf("line kept unsafe characters: %q", got)
	}
	if got := clean("a\nb\tc\u2066\x00"); got != "a\nb\tc" {
		t.Errorf("clean = %q", got)
	}
}

func TestLineTurnsUnicodeLineSeparatorsIntoSpaces(t *testing.T) {
	if got := line("a\u2028b\u2029c"); got != "a b c" {
		t.Errorf("line = %q, want %q", got, "a b c")
	}
}

func TestAPIErrorWithoutAnEnvelopeNamesOnlyTheStatus(t *testing.T) {
	err := apiError(&http.Response{StatusCode: http.StatusBadGateway}, []byte("<html>http://10.0.0.1/internal</html>"))
	if got := err.Error(); got != "the knowledge server answered 502 Bad Gateway" {
		t.Errorf("error = %q", got)
	}
}

func TestTransportErrorDropsTheCause(t *testing.T) {
	err := transportError(errors.New("dial tcp 10.0.0.1:8080: connection refused"))
	if strings.Contains(err.Error(), "10.0.0.1") {
		t.Errorf("error leaks the address: %v", err)
	}
}
