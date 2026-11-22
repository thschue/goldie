package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #150: the CLI's API client says who it is
// (X-Goldie-Client), follows no redirects, and its usage text and docs
// recommend the token variables over --token.

// recorded is one request a recording server saw.
type recorded struct {
	path   string
	client []string // X-Goldie-Client values
	auth   string
}

// recorder is a stub API that records every request and answers each route
// with the smallest body the CLI accepts.
type recorder struct {
	mu   sync.Mutex
	reqs []recorded
}

func (rec *recorder) seen() []recorded {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]recorded(nil), rec.reqs...)
}

func recordingServer(t *testing.T) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, recorded{path: r.URL.Path, client: r.Header.Values("X-Goldie-Client"), auth: r.Header.Get("Authorization")})
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/openapi.json":
			_, _ = w.Write([]byte(`{"info":{"title":"Stub","version":"1"}}`))
		case r.URL.Query().Has("q"):
			_, _ = w.Write([]byte(`{"results":[]}`))
		default:
			_, _ = w.Write([]byte(`{"id":"x","name":"X","keywords":["k"],"content":"body"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// setVersion sets platform.Version, as -ldflags does in a release build, for
// the length of the test.
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := platform.Version
	platform.Version = v
	t.Cleanup(func() { platform.Version = old })
}

// Every request of every client command, with or without a token and to the
// public API document as well as the token routes, carries exactly one
// X-Goldie-Client: goldie/<platform.Version>.
func TestCLISendsGoldieClientOnEveryRequest(t *testing.T) {
	commands := []struct {
		name string
		args []string
		min  int // requests the command makes at least
	}{
		{"search", []string{"search", "kubernetes", "--token", cliToken}, 1},
		{"lookup", []string{"lookup", "platform/deploy", "--token", cliToken}, 1},
		{"status with token", []string{"status", "--token", cliToken}, 2},
		{"status without token", []string{"status"}, 1},
		{"version", []string{"version"}, 1},
		{"search --json", []string{"search", "x", "--json", "--token", cliToken}, 1},
	}
	// Two versions, so the value follows platform.Version and is not a
	// constant that happens to match.
	for _, version := range []string{"1.2.3-acceptance", "0.0.0-other+build.7"} {
		for _, c := range commands {
			t.Run(version+"/"+c.name, func(t *testing.T) {
				setVersion(t, version)
				srv, rec := recordingServer(t)
				if _, err := runCLI(t, append(c.args, "--addr", srv.URL)...); err != nil {
					t.Fatalf("%v: %v", c.args, err)
				}
				reqs := rec.seen()
				if len(reqs) < c.min {
					t.Fatalf("%s made %d requests, want at least %d", c.name, len(reqs), c.min)
				}
				want := "goldie/" + version
				for _, r := range reqs {
					if len(r.client) != 1 || r.client[0] != want {
						t.Errorf("%s: request to %s carries X-Goldie-Client %q, want exactly [%q]", c.name, r.path, r.client, want)
					}
				}
			})
		}
	}
}

// The header names the client, never the token, and adding it does not put
// the token on the public API document.
func TestCLIGoldieClientCarriesNoToken(t *testing.T) {
	setVersion(t, "1.2.3-acceptance")
	srv, rec := recordingServer(t)
	for _, cmd := range []string{"status", "version"} {
		if _, err := runCLI(t, cmd, "--addr", srv.URL, "--token", cliToken); err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
	}
	for _, r := range rec.seen() {
		for _, v := range r.client {
			if strings.Contains(v, cliToken) {
				t.Errorf("X-Goldie-Client on %s carries the token: %q", r.path, v)
			}
		}
		if r.path == "/openapi.json" && r.auth != "" {
			t.Errorf("/openapi.json got Authorization %q, want none", r.auth)
		}
	}
}

// redirectTo answers every request with code and a Location of target plus
// the request's path, and body: an error envelope (JSON) or HTML.
func redirectTo(t *testing.T, code int, target func(*http.Request) string, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/landing" || strings.HasPrefix(r.URL.Path, "/landing/") {
			// The same-server target: reaching it means the redirect was followed.
			t.Errorf("same-server redirect target %s was requested (Authorization %q)", r.URL.Path, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Location", target(r))
		ctype := "text/html; charset=utf-8" // what http.Redirect sends
		if strings.HasPrefix(body, "{") {
			ctype = "application/json"
		}
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

var redirectCodes = []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect}

// The CLI follows no redirect: a server that redirects to another server on
// the same host (another port), or to another path on itself, never sees the
// target requested, so the token cannot reach it. Go's default client would
// forward Authorization in both cases.
func TestCLIFollowsNoRedirectOnTheSameHost(t *testing.T) {
	target, rec := recordingServer(t)
	commands := [][]string{
		{"search", "agent", "--token", cliToken},
		{"lookup", "platform/deploy", "--token", cliToken},
		{"status", "--token", cliToken},
		{"version"},
	}
	for _, code := range redirectCodes {
		for name, to := range map[string]func(*http.Request) string{
			"other port":  func(r *http.Request) string { return target.URL + r.URL.RequestURI() },
			"same server": func(r *http.Request) string { return "/landing" + r.URL.RequestURI() },
		} {
			srv := redirectTo(t, code, to, `{}`)
			for _, args := range commands {
				t.Run(http.StatusText(code)+"/"+name+"/"+args[0], func(t *testing.T) {
					_, _ = runCLI(t, append(args, "--addr", srv.URL)...)
				})
			}
		}
	}
	for _, r := range rec.seen() {
		t.Errorf("redirect target was requested: %s with Authorization %q", r.path, r.auth)
	}
}

// A redirect answer is an error the CLI words itself: the same text whatever
// the Location and the body say, with no Location, address, credential or
// token in it, and nothing on stdout.
func TestCLIRedirectIsAFixedErrorThatEchoesNothing(t *testing.T) {
	const leakToken = "leak-token-0123456789abcdef0123456789abcd"
	variants := []struct{ location, body string }{
		{"http://leak-user:leak-pass@127.0.0.1:1/leak-path?token=" + leakToken,
			`{"error":{"code":"moved","message":"leak-body-message see http://127.0.0.1:1/leak-path","details":{"hint":"leak-hint ` + leakToken + `"}}}`},
		{"https://leak-other.invalid/leak-second?access_token=" + leakToken + "#leak-fragment",
			`{"error":{"code":"moved","message":"leak-another-message","details":{"hint":"leak-another-hint"}}}`},
		{"/leak-relative-path?token=" + leakToken, `<a href="/leak-relative-path">leak-html-body</a>.`},
	}
	for _, args := range [][]string{
		{"search", "agent"},
		{"lookup", "platform/deploy"},
		{"status"},
	} {
		t.Run(args[0], func(t *testing.T) {
			var first string
			for i, v := range variants {
				srv := redirectTo(t, http.StatusFound, func(*http.Request) string { return v.location }, v.body)
				out, err := runCLI(t, append(args, "--addr", srv.URL, "--token", cliToken)...)
				if err == nil {
					t.Fatalf("variant %d: %v on a 302 succeeded with %q, want an error", i, args, out)
				}
				if out != "" {
					t.Errorf("variant %d: stdout = %q, want nothing on a redirect", i, out)
				}
				msg := err.Error()
				if strings.Contains(msg, "cannot reach") {
					t.Errorf("variant %d: error %q says the server is unreachable, but it answered with a redirect", i, msg)
				}
				for _, bad := range []string{"leak", cliToken, leakToken, "127.0.0.1", srv.URL} {
					if strings.Contains(msg, bad) {
						t.Errorf("variant %d: error %q echoes %q", i, msg, bad)
					}
				}
				if i == 0 {
					first = msg
				} else if msg != first {
					t.Errorf("variant %d: error %q differs from %q; a redirect must give one fixed error", i, msg, first)
				}
			}
		})
	}
}

// processListing is the reason a --token value is exposed: it shows in
// process listings (or the process table, or ps). A bare "process" is not
// enough: "subprocess" already appears in the MCP docs.
var processListing = regexp.MustCompile(`(?i)\bprocess(es)?\s+(list|table)|\bps\b`)

// tokenRecommendation checks that text recommends the token variables and
// says why: a token on the command line shows in process listings.
func tokenRecommendation(t *testing.T, where, text string) {
	t.Helper()
	if !strings.Contains(text, "GOLDIE_TOKEN_FILE") {
		t.Errorf("%s does not name GOLDIE_TOKEN_FILE:\n%s", where, text)
	}
	if !strings.Contains(strings.ReplaceAll(text, "GOLDIE_TOKEN_FILE", ""), "GOLDIE_TOKEN") {
		t.Errorf("%s does not name GOLDIE_TOKEN:\n%s", where, text)
	}
	if !processListing.MatchString(text) {
		t.Errorf("%s does not say why (a --token value shows in process listings):\n%s", where, text)
	}
}

// The CLI's usage text recommends GOLDIE_TOKEN or GOLDIE_TOKEN_FILE over
// --token and says why, and still lists --token.
func TestCLIUsageRecommendsTokenVariables(t *testing.T) {
	for name, args := range map[string][]string{
		"no command":  nil,
		"search help": {"search", "--help"},
		"status help": {"status", "--help"},
		"bad flag":    {"lookup", "x", "--bogus"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runCLI(t, args...)
			if err == nil {
				t.Fatal("expected a usage error")
			}
			tokenRecommendation(t, "usage of goldie "+strings.Join(args, " "), err.Error())
			if !strings.Contains(err.Error(), "--token") {
				t.Errorf("usage no longer lists --token, which keeps working:\n%s", err)
			}
		})
	}
}

// goldie mcp's usage text recommends the variables too, and names the second
// reason: an MCP client saves the arguments it starts goldie with in its config.
func TestMCPUsageRecommendsTokenVariables(t *testing.T) {
	// Build the binary before runMCPExpectingFailure starts its 10-second
	// timer: this may be the first test to need it, and under -race on a cold
	// cache the non-race build alone can take longer than that.
	goldieBinary(t)
	for name, transport := range map[string][]string{
		"help":         {"--help"},
		"no transport": {},
	} {
		t.Run(name, func(t *testing.T) {
			_, stderr := runMCPExpectingFailure(t, nil, nil, transport...)
			tokenRecommendation(t, "goldie mcp usage", stderr)
			if !strings.Contains(strings.ToLower(stderr), "config") {
				t.Errorf("goldie mcp usage does not say a --token value is saved in the MCP client's config:\n%s", stderr)
			}
			if !strings.Contains(stderr, "--token") {
				t.Errorf("goldie mcp usage no longer lists --token, which keeps working:\n%s", stderr)
			}
			if strings.Contains(stderr, cliToken) {
				t.Errorf("usage carries a token: %q", stderr)
			}
		})
	}
}

// readmeSection returns the README section under the level-2 heading.
func readmeSection(t *testing.T, heading string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(b), "\n## "+heading+"\n")
	if !ok {
		t.Fatalf("README.md has no section %q", heading)
	}
	section, _, _ := strings.Cut(rest, "\n## ")
	return section
}

// The docs of the CLI and of goldie mcp recommend the variables over --token
// and say why.
func TestDocsRecommendTokenVariables(t *testing.T) {
	cli := readmeSection(t, "CLI")
	tokenRecommendation(t, "README section CLI", cli)
	mcp := readmeSection(t, "MCP over stdio")
	tokenRecommendation(t, "README section MCP over stdio", mcp)
	if !strings.Contains(strings.ToLower(mcp), "config") {
		t.Errorf("README section MCP over stdio does not say a --token value is saved in the MCP client's config:\n%s", mcp)
	}
	for name, s := range map[string]string{"CLI": cli, "MCP over stdio": mcp} {
		if !strings.Contains(s, "--token") {
			t.Errorf("README section %s no longer documents --token, which keeps working", name)
		}
	}
}

// --token keeps working: it authenticates, and it still overrides the
// variables.
func TestCLITokenFlagStillAuthenticates(t *testing.T) {
	srv := cliServer(t)
	out, err := runCLI(t, "search", "agent", "--addr", srv.URL, "--token", cliToken)
	if err != nil {
		t.Fatalf("search --token: %v", err)
	}
	if !strings.Contains(out, "agent-loops") {
		t.Errorf("search --token found nothing: %q", out)
	}
	if _, err := runCLI(t, "lookup", "platform/deploy", "--addr", srv.URL, "--token", "wrong-token-value"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("lookup with a wrong --token: err = %v, want the 401", err)
	}
	t.Chdir(t.TempDir())
	t.Setenv("GOLDIE_TOKEN", "wrong-token-value")
	_ = os.Unsetenv("GOLDIE_TOKEN_FILE")
	t.Setenv("GOLDIE_ADDR", srv.URL)
	var buf strings.Builder
	if err := run([]string{"lookup", "platform/deploy", "--token", cliToken}, &buf); err != nil {
		t.Errorf("--token must override GOLDIE_TOKEN: %v", err)
	}
}
