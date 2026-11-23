package engine_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// TestGitOptionsNeverPrintAQueryOrFragment formats and logs GitOptions whose
// URL carries a token in its query string or fragment. NewGitSource refuses
// such a URL, but the options can be logged before they are validated. Since
// #96 GitOptions never prints the URL at all, only whether it is set: the
// host and path are gone too, and the Ref stays.
func TestGitOptionsNeverPrintAQueryOrFragment(t *testing.T) {
	const secret = "glpat-query0123456789"
	for _, raw := range []string{
		"https://git.example.invalid/r.git?private_token=" + secret,
		"https://git.example.invalid/r.git#" + secret,
		"https://git.example.invalid/r.git?a=" + secret + "#" + secret,
	} {
		opts := engine.GitOptions{URL: raw, Ref: "main"}
		var logs bytes.Buffer
		slog.New(slog.NewTextHandler(&logs, nil)).Info("source", "opts", opts)
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("source", "opts", opts)
		for _, s := range []string{fmt.Sprintf("%v %+v %#v %s", opts, opts, opts, opts), logs.String()} {
			assertNoSecret(t, s, secret, "git.example.invalid", "r.git")
			if !strings.Contains(s, "main") {
				t.Fatalf("redaction dropped the Ref: %q", s)
			}
		}
	}
}
