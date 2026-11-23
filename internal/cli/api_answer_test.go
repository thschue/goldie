package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #236: the CLI reads every API answer one way, so a
// 200 that is not a goldie API answer (a proxy's login page, say) gets one
// precise message in search, lookup and the status token probe, and every
// other message stays as it was. The cut is cli.Run against a stub server.

// notAPIAnswerWording is the whole error for a 200 without a JSON content type.
const notAPIAnswerWording = "the server's answer isn't a goldie API answer; check the address"

// loginPage is a hostile proxy login page: an address, a marker, a terminal
// escape and the word "token", none of which may reach the user.
const loginPage = "<html><body>Sign in at http://10.0.0.7:3128/login?next=leak-marker \x1b[2J token</body></html>"

// remoteRun runs args against a remote server at addr, with the test token.
func remoteRun(t *testing.T, addr string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	env := localEnv(map[string]string{platform.EnvToken: localTestToken}, &out)
	err := cli.Run(context.Background(), append(append([]string{}, args...), "--addr", addr), env)
	return out.String(), err
}

// notAPIAnswer answers 200 with a body under contentType; an empty
// contentType sends no Content-Type header at all.
func notAPIAnswer(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if contentType == "" {
			w.Header()["Content-Type"] = nil // no sniffing either
		} else {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
}

// notAPIAnswers are 200s that are not API answers: a login page, plain text,
// no content type, and a well-formed search answer under a non-JSON type.
var notAPIAnswers = map[string]http.HandlerFunc{
	"text/html login page":       notAPIAnswer("text/html; charset=utf-8", loginPage),
	"text/plain":                 notAPIAnswer("text/plain", "OK leak-marker http://10.0.0.7/"),
	"no content type":            notAPIAnswer("", loginPage),
	"JSON body under text/plain": notAPIAnswer("text/plain", `{"query":"x","results":[]}`),
}

// requireNotAPIAnswer checks err is exactly notAPIAnswerWording, and so
// carries no address, marker, escape, status text or token.
func requireNotAPIAnswer(t *testing.T, err error, addr string) {
	t.Helper()
	if err == nil {
		t.Fatal("no error for a 200 that is not an API answer")
	}
	msg := err.Error()
	if msg != notAPIAnswerWording {
		t.Errorf("error = %q, want %q", msg, notAPIAnswerWording)
	}
	for _, bad := range []string{addr, strings.TrimPrefix(addr, "http://"), "10.0.0.7", "leak-marker", "\x1b", "200 OK", localTestToken, "embedded"} {
		if strings.Contains(msg, bad) {
			t.Errorf("error carries %q: %q", bad, msg)
		}
	}
}

func TestCLIA200WithoutAJSONContentTypeIsNotAGoldieAPIAnswer(t *testing.T) {
	for name, h := range notAPIAnswers {
		t.Run(name+"/search", func(t *testing.T) {
			srv := httptest.NewServer(h)
			t.Cleanup(srv.Close)
			out, err := remoteRun(t, srv.URL, "search", "x")
			requireNotAPIAnswer(t, err, srv.URL)
			if out != "" {
				t.Errorf("search printed %q for an answer it could not read", out)
			}
		})
		t.Run(name+"/lookup", func(t *testing.T) {
			srv := httptest.NewServer(h)
			t.Cleanup(srv.Close)
			out, err := remoteRun(t, srv.URL, "lookup", "platform/deploy")
			requireNotAPIAnswer(t, err, srv.URL)
			if out != "" {
				t.Errorf("lookup printed %q for an answer it could not read", out)
			}
		})
		t.Run(name+"/status token probe", func(t *testing.T) {
			srv := openAPIThen(t, h)
			_, err := remoteRun(t, srv.URL, "status")
			requireNotAPIAnswer(t, err, srv.URL)
		})
	}
}

// The same holds with --json: the error is the same and nothing is printed.
func TestCLIA200WithoutAJSONContentTypeIsNotAGoldieAPIAnswerWithJSONOutput(t *testing.T) {
	srv := httptest.NewServer(notAPIAnswers["text/html login page"])
	t.Cleanup(srv.Close)
	for _, args := range [][]string{{"search", "x", "--json"}, {"lookup", "platform/deploy", "--json"}} {
		out, err := remoteRun(t, srv.URL, args...)
		requireNotAPIAnswer(t, err, srv.URL)
		if out != "" {
			t.Errorf("%s --json printed %q", args[0], out)
		}
	}
}

// Negative: a JSON answer with parameters on its content type is an API
// answer and is shown.
func TestCLIJSONAnswerWithACharsetIsShown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if strings.HasSuffix(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"query":"x","results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"platform/deploy","name":"Deploy","keywords":["k"],"content":"body"}`))
	}))
	t.Cleanup(srv.Close)
	if out, err := remoteRun(t, srv.URL, "search", "x"); err != nil || !strings.Contains(out, `No articles match "x".`) {
		t.Errorf("search: err %v, output %q", err, out)
	}
	if out, err := remoteRun(t, srv.URL, "lookup", "platform/deploy"); err != nil || !strings.HasPrefix(out, "Deploy\nid: platform/deploy\n") {
		t.Errorf("lookup: err %v, output %q", err, out)
	}
}

// answerCase is a server failure and the exact message it gets today.
type answerCase struct {
	name    string
	handler http.HandlerFunc
	want    string
}

func envelope(status int, header map[string]string, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// oversized answers 200 with a JSON search answer above the 16 MiB cap.
func oversized(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	head := []byte(`{"query":"x","results":[]}`)
	_, _ = w.Write(append(head, bytes.Repeat([]byte(" "), 16<<20+1-len(head))...))
}

var remoteAnswerCases = []answerCase{
	{"redirect", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", redirectLeakLocation)
		w.WriteHeader(http.StatusFound)
	}, "the server answered with a redirect, which the CLI does not follow: check --addr or GOLDIE_ADDR"},
	{"too large", oversized, "the server's answer is larger than 16 MiB"},
	{"unreadable", truncatedAnswer, "cannot reach the server: check --addr or GOLDIE_ADDR and that the server is running"},
	{"envelope with hint", envelope(http.StatusNotFound, map[string]string{"Content-Type": "application/json"},
		`{"error":{"code":"not_found","message":"no such article","details":{"hint":"find ids with search"}}}`),
		"no such article (404 Not Found)\nHint: find ids with search"},
	{"429 with a wait", envelope(http.StatusTooManyRequests, map[string]string{"Retry-After": "30"}, ""),
		"the server answered 429 Too Many Requests\nHint: Wait 30 seconds, then retry."},
	{"429 without a wait", envelope(http.StatusTooManyRequests, nil, ""),
		"the server answered 429 Too Many Requests\nHint: Wait a little, then retry."},
	{"502 HTML page", envelope(http.StatusBadGateway, map[string]string{"Content-Type": "text/html"}, loginPage),
		"the server answered 502 Bad Gateway"},
}

// Criterion: every other existing CLI message is unchanged, in search, lookup
// and the status token probe.
func TestCLIAnswerFailureMessagesAreUnchanged(t *testing.T) {
	for _, c := range remoteAnswerCases {
		for _, args := range [][]string{{"search", "x"}, {"lookup", "platform/deploy"}, {"status"}} {
			t.Run(c.name+"/"+args[0], func(t *testing.T) {
				var srv *httptest.Server
				if args[0] == "status" {
					srv = openAPIThen(t, c.handler)
				} else {
					srv = httptest.NewServer(c.handler)
					t.Cleanup(srv.Close)
				}
				_, err := remoteRun(t, srv.URL, args...)
				if err == nil {
					t.Fatal("no error")
				}
				if got := err.Error(); got != c.want {
					t.Errorf("error = %q, want %q", got, c.want)
				}
			})
		}
	}
	t.Run("unreachable", func(t *testing.T) {
		dead := closedAddr(t)
		for _, args := range [][]string{{"search", "x"}, {"lookup", "platform/deploy"}, {"status"}} {
			_, err := remoteRun(t, dead, args...)
			want := "cannot reach the server: check --addr or GOLDIE_ADDR and that the server is running"
			if err == nil || err.Error() != want {
				t.Errorf("%s: error = %v, want %q", args[0], err, want)
			}
		}
	})
}

// Criterion: the list joiner is the same for every list: each entry cleaned
// to one line, joined with ", ", and "(none)" for an empty list.
func TestCLIListsAreCleanedAndJoinedOneWay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"query":"x","results":[{"id":"a","kind":"article","name":"A","score":3,` +
				`"matched":["one\nkeywords: forged","two\u202e"],"keywords":[]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"a","name":"A","keywords":["k1","k\u200b2\tx"],"content":"body"}`))
	}))
	t.Cleanup(srv.Close)
	out, err := remoteRun(t, srv.URL, "search", "x")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, want := range []string{"    score 3, matched: one keywords: forged, two\n", "    keywords: (none)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("search output lacks %q:\n%s", want, out)
		}
	}
	out, err = remoteRun(t, srv.URL, "lookup", "a")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !strings.Contains(out, "\nkeywords: k1, k2 x\n") {
		t.Errorf("lookup output lacks the joined keywords:\n%s", out)
	}
}
