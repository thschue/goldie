package app

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/thschue/goldie/internal/cli"
)

// Acceptance test for #128: the CLI and the MCP component clean server text
// identically, so the two can no longer drift. The same hostile table goes
// through both at their seams: `goldie lookup` and the error it returns, and
// the get_article tool and its tool error. Each field is wrapped in the
// markers below, so the cleaned value can be cut out of either client's
// wording, which stays its own.

const (
	markOpen  = "\u27e6" // MATHEMATICAL LEFT WHITE SQUARE BRACKET: visible, not Cf
	markClose = "\u27e7"
)

var driftCases = []struct {
	name string
	in   string
	text string // a multi-line field: the article's content
	line string // a single-line field: name, error message, hint
}{
	{"visible text is kept", "Gr\u00fc\u00dfe \u65e5\u672c e\u0301 <b>`x`</b>", "Gr\u00fc\u00dfe \u65e5\u672c e\u0301 <b>`x`</b>", "Gr\u00fc\u00dfe \u65e5\u672c e\u0301 <b>`x`</b>"},
	{"newline and tab", "a\nb\tc", "a\nb\tc", "a b c"},
	{"Unicode line separators", "a\u2028b\u2029c", "a\u2028b\u2029c", "a b c"},
	{"C0 controls and escapes", "a\x00b\x07c\x1b[31md\re", "abc[31mde", "abc[31mde"},
	{"DEL", "a\x7fb", "ab", "ab"},
	{"C1 controls", "a\u0080b\u0085c\u009bd", "abcd", "abcd"},
	{"bidirectional controls", "a\u202eb\u2066c\u2069d\u200ee\u200ff\u061cg", "abcdefg", "abcdefg"},
	{"zero-width characters and BOM", "a\u200bb\u200cc\u200dd\u2060e\ufefff", "abcdef", "abcdef"},
	{"tag characters", "a\U000E0001b\U000E0049\U000E0047c", "abc", "abc"},
	{"other format characters", "a\u00adb\u180ec\ufff9d", "abcd", "abcd"},
}

// fields cuts every marked value out of s, in order.
func fields(s string) []string {
	var out []string
	for {
		i := strings.Index(s, markOpen)
		if i < 0 {
			return out
		}
		s = s[i+len(markOpen):]
		j := strings.Index(s, markClose)
		if j < 0 {
			return append(out, "<unterminated>"+s)
		}
		out = append(out, s[:j])
		s = s[j+len(markClose):]
	}
}

func hostile(t *testing.T, status int, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// runCLI runs a client command against url with the test token.
func runCLI(t *testing.T, url string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	env := cli.Env{
		Lookup:   func(string) (string, bool) { return "", false },
		ReadFile: os.ReadFile,
		Out:      &out,
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	exec := cli.Command(args[0], fs)
	pf := pflag.NewFlagSet(args[0], pflag.ContinueOnError)
	pf.SetOutput(io.Discard)
	pf.AddGoFlagSet(fs)
	if err := pf.Parse(append(args[1:], "--addr", url, "--token", testToken)); err != nil {
		return "", err
	}
	err := exec(context.Background(), pf.Args(), env)
	return out.String(), err
}

func TestCLIAndMCPCleanArticleTextIdentically(t *testing.T) {
	for _, c := range driftCases {
		t.Run(c.name, func(t *testing.T) {
			marked := markOpen + c.in + markClose
			url := hostile(t, http.StatusOK, map[string]any{
				"id": "x", "name": marked, "keywords": []string{}, "content": marked,
			})
			want := []string{c.line, c.text}

			cliOut, err := runCLI(t, url, "lookup", "x")
			if err != nil {
				t.Fatalf("cli lookup: %v", err)
			}
			mcpOut, isErr := call(t, session(t, url, testToken), "get_article", map[string]any{"id": "x"})
			if isErr {
				t.Fatalf("mcp get_article: tool error %q", mcpOut)
			}
			gotCLI, gotMCP := fields(cliOut), fields(mcpOut)
			if !slices.Equal(gotCLI, gotMCP) {
				t.Errorf("cli and mcp drift:\ncli %q\nmcp %q", gotCLI, gotMCP)
			}
			if !slices.Equal(gotCLI, want) {
				t.Errorf("cli name and content = %q, want %q", gotCLI, want)
			}
			if !slices.Equal(gotMCP, want) {
				t.Errorf("mcp name and content = %q, want %q", gotMCP, want)
			}
		})
	}
}

func TestCLIAndMCPCleanErrorEnvelopesIdentically(t *testing.T) {
	for _, c := range driftCases {
		t.Run(c.name, func(t *testing.T) {
			marked := markOpen + c.in + markClose
			url := hostile(t, http.StatusNotFound, map[string]any{
				"error": map[string]any{"code": "not_found", "message": marked, "details": map[string]any{"hint": marked}},
			})
			want := []string{c.line, c.line}

			_, err := runCLI(t, url, "lookup", "x")
			if err == nil {
				t.Fatal("cli lookup: expected an error")
			}
			mcpOut, isErr := call(t, session(t, url, testToken), "get_article", map[string]any{"id": "x"})
			if !isErr {
				t.Fatalf("mcp get_article: want a tool error, got %q", mcpOut)
			}
			gotCLI, gotMCP := fields(err.Error()), fields(mcpOut)
			if !slices.Equal(gotCLI, gotMCP) {
				t.Errorf("cli and mcp drift:\ncli %q\nmcp %q", gotCLI, gotMCP)
			}
			if !slices.Equal(gotCLI, want) {
				t.Errorf("cli message and hint = %q, want %q", gotCLI, want)
			}
			if !slices.Equal(gotMCP, want) {
				t.Errorf("mcp message and hint = %q, want %q", gotMCP, want)
			}
		})
	}
}
