package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

func TestServerTextCannotForgeLinesOrHideCharacters(t *testing.T) {
	if got := contract.CleanLine("name\n- id: forged\u202e\x1b[2J"); strings.ContainsAny(got, "\n\u202e\x1b") {
		t.Errorf("CleanLine kept unsafe characters: %q", got)
	}
	if got := contract.CleanText("a\nb\tc\u2066\x00"); got != "a\nb\tc" {
		t.Errorf("CleanText = %q", got)
	}
}

func TestServerTextCannotSmuggleInvisibleFormatCharacters(t *testing.T) {
	// Zero-width characters, the byte-order mark, the word joiner and Unicode
	// tag characters are invisible to a person reading the Source, but the
	// agent reads them.
	hidden := "a\u200bb\u200cc\u200dd\u2060e\ufefff\U000E0049\U000E0047g"
	for name, f := range map[string]func(string) string{"CleanText": contract.CleanText, "CleanLine": contract.CleanLine} {
		if got := f(hidden); got != "abcdefg" {
			t.Errorf("%s = %q, want %q", name, got, "abcdefg")
		}
	}
}

func TestLineTurnsUnicodeLineSeparatorsIntoSpaces(t *testing.T) {
	if got := contract.CleanLine("a\u2028b\u2029c"); got != "a b c" {
		t.Errorf("CleanLine = %q, want %q", got, "a b c")
	}
}

func TestAPIErrorWithoutAnEnvelopeNamesOnlyTheStatus(t *testing.T) {
	err := apiError(&http.Response{StatusCode: http.StatusBadGateway}, []byte("<html>http://10.0.0.1/internal</html>"))
	if got := err.Error(); got != "the knowledge server answered 502 Bad Gateway" {
		t.Errorf("error = %q", got)
	}
}

func TestTransportErrorNamesCancellationAndTimeout(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := transportError(fmt.Errorf("Get \"http://10.0.0.1:8080/api/v1/search\": %w", cause))
		if got := err.Error(); got != "the request to the knowledge server was cancelled or timed out" {
			t.Errorf("%v: error = %q", cause, got)
		}
	}
}

func TestTransportErrorDropsTheCause(t *testing.T) {
	err := transportError(errors.New("dial tcp 10.0.0.1:8080: connection refused"))
	if strings.Contains(err.Error(), "10.0.0.1") {
		t.Errorf("error leaks the address: %v", err)
	}
}
