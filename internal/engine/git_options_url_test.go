package engine_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// printed is opts formatted and logged every way a caller might: %v, %+v,
// %#v, %s, and slog's text and JSON handlers.
func printed(opts engine.GitOptions) string {
	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{ReplaceAttr: dropTime})).Info("source", "opts", opts)
	slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{ReplaceAttr: dropTime})).Info("source", "opts", opts)
	return fmt.Sprintf("%v|%+v|%#v|%s|", opts, opts, opts, opts) + logs.String()
}

func dropTime(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

// #96 (folded in from #392): GitOptions never prints the URL. String,
// GoString and LogValue show only whether it is set, so every set URL prints
// the same, an unset one prints differently, and no scheme, user, host, port
// or path reaches the output, whatever url.Parse makes of the URL. The URL is
// logged only by app, through platform's redactor.
func TestGitOptionsNeverPrintTheURL(t *testing.T) {
	urls := []string{
		"https://git.example.invalid/org/r.git",
		"https://deploy:pw-c4n4ry@git.example.invalid:8443/org/r.git",
		"ssh://deploy-c4n4ry@git.example.invalid:2222/org/r.git",
		"git@git.example.invalid:org/r.git",
		"deploy:/pw-c4n4ry@git.example.invalid:org/r.git",
		"file:///srv/c4n4ry/org/r.git",
		"ext::sh -c touch% /tmp/c4n4ry",
		"https://git.example.invalid/org/r.git\n\"}{\"x\":\"c4n4ry\"}",
		"https://git.example.invalid/org/r.git\u202ec4n4ry",
		"-oProxyCommand=c4n4ry",
		"%zz",
	}
	unset := printed(engine.GitOptions{Ref: "main"})
	want := printed(engine.GitOptions{URL: urls[0], Ref: "main"})
	if want == unset {
		t.Fatalf("a set URL prints like an unset one; it must show whether it is set:\n%s", want)
	}
	for _, raw := range urls {
		got := printed(engine.GitOptions{URL: raw, Ref: "main"})
		for _, part := range []string{"git.example.invalid", "org/r.git", "c4n4ry", "deploy", "srv", "ext::", "8443", "2222", "ProxyCommand", "%zz"} {
			if strings.Contains(got, part) {
				t.Errorf("GitOptions{URL: %q} prints %q:\n%s", raw, part, got)
			}
		}
		if got != want {
			t.Errorf("GitOptions{URL: %q} prints differently from another set URL, so it shows more than whether it is set:\n got %s\nwant %s", raw, got, want)
		}
		if !strings.Contains(got, "main") {
			t.Errorf("GitOptions{URL: %q} dropped the Ref:\n%s", raw, got)
		}
	}
}
