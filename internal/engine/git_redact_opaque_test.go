package engine_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// url.Parse reads "usr:pw@host:path" (an scp-like git URL with a password) as
// the scheme "usr" with the opaque rest "pw@host:path", so it has no User to
// redact. Formatting or logging GitOptions must still not print the password.
func TestGitOptionsNeverPrintAnOpaqueURLsPassword(t *testing.T) {
	const secret = "pw-opaque0123456789"
	for _, raw := range []string{
		"deploy:" + secret + "@git.example.invalid:org/r.git",
		"git:" + secret + "@git.example.invalid:2222/org/r.git",
		"mailto:" + secret + "@git.example.invalid",
	} {
		opts := engine.GitOptions{URL: raw, Ref: "main"}
		var logs bytes.Buffer
		slog.New(slog.NewTextHandler(&logs, nil)).Info("source", "opts", opts)
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("source", "opts", opts)
		assertNoSecret(t, fmt.Sprintf("%v %+v %#v %s", opts, opts, opts, opts)+logs.String(), secret)
	}
}
