package cli_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Acceptance tests for #236, review round 1: status's first request (the API
// document) reads a 200 that is not an API answer like every other request,
// and in local mode (--dir) the CLI words that answer and a redirect as the
// embedded server's, with no address hint.

// localNotAPIAnswerWording is the whole error for a 200 without a JSON content
// type from the embedded server.
const localNotAPIAnswerWording = "the embedded server's answer isn't a goldie API answer"

// requireNoAddressHint checks a local-mode error carries no address hint, and
// none of the address, the token or the answer.
func requireNoAddressHint(t *testing.T, msg, addr string) {
	t.Helper()
	low := strings.ToLower(msg)
	for _, bad := range []string{"address", "--addr", "goldie_addr", strings.ToLower(addr), strings.TrimPrefix(addr, "http://"),
		"10.0.0.7", "leak-", "\x1b", "200 ok", strings.ToLower(localTestToken)} {
		if strings.Contains(low, bad) {
			t.Errorf("error carries %q: %q", bad, msg)
		}
	}
}

// Criterion: a non-JSON 200 on status's first request, the API document, is
// not a goldie API answer, with and without --json, and nothing is printed.
func TestCLIStatusNonJSON200APIDocumentIsNotAGoldieAPIAnswer(t *testing.T) {
	for name, h := range notAPIAnswers {
		for _, args := range [][]string{{"status"}, {"status", "--json"}} {
			t.Run(name+"/"+strings.Join(args, " "), func(t *testing.T) {
				srv := httptest.NewServer(h) // every path, the API document included
				t.Cleanup(srv.Close)
				out, err := remoteRun(t, srv.URL, args...)
				requireNotAPIAnswer(t, err, srv.URL)
				if out != "" {
					t.Errorf("status printed %q for an API document it could not read", out)
				}
			})
		}
	}
}

// localCases are the requests of each command in local mode: the command's
// own request, and for status the API document and the token probe.
var localCases = []struct {
	name string
	args []string
	srv  func(t *testing.T, h http.HandlerFunc) *httptest.Server
}{
	{"search", []string{"search", "x", "--dir", "/content"}, plainServer},
	{"lookup", []string{"lookup", "platform/deploy", "--dir", "/content"}, plainServer},
	{"status API document", []string{"status", "--dir", "/content"}, plainServer},
	{"status token probe", []string{"status", "--dir", "/content"}, openAPIThen},
	{"search --json", []string{"search", "x", "--json", "--dir", "/content"}, plainServer},
	{"status --json", []string{"status", "--json", "--dir", "/content"}, plainServer},
}

func plainServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// Criterion: in local mode a non-JSON 200 is the embedded server's answer
// that isn't a goldie API answer, with no address hint.
func TestLocalModeNonJSON200AnswerIsTheEmbeddedServers(t *testing.T) {
	for name, h := range notAPIAnswers {
		for _, c := range localCases {
			t.Run(name+"/"+c.name, func(t *testing.T) {
				srv := c.srv(t, h)
				err := localRun(t, srv.URL, c.args...)
				if err == nil {
					t.Fatal("no error for a 200 that is not an API answer")
				}
				if got := err.Error(); got != localNotAPIAnswerWording {
					t.Errorf("error = %q, want %q", got, localNotAPIAnswerWording)
				}
				requireNoAddressHint(t, err.Error(), srv.URL)
			})
		}
	}
}

// Criterion: in local mode a redirect on any request is worded exactly as the
// embedded server's, with no address hint and nothing of the Location.
func TestLocalModeRedirectIsWordedExactly(t *testing.T) {
	redirect := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", redirectLeakLocation)
		w.WriteHeader(http.StatusFound)
	}
	for _, c := range localCases {
		t.Run(c.name, func(t *testing.T) {
			srv := c.srv(t, redirect)
			err := localRun(t, srv.URL, c.args...)
			if err == nil {
				t.Fatal("no error for a redirect")
			}
			if got := err.Error(); got != localRedirectWording {
				t.Errorf("error = %q, want %q", got, localRedirectWording)
			}
			requireNoAddressHint(t, err.Error(), srv.URL)
		})
	}
}
