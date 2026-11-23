package contract_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #236: contract reads an API answer one way, so the CLI
// and goldie mcp only word what it found. The API they pin:
//
//	func Answer[R, B any](raw *http.Response, err error, parse func(*http.Response) (R, error), body func(R) *B) (*B, error)
//	var ErrNotAPIAnswer error // a 200 that is not an API answer
//	func (e *APIError) Error() string
//	func CleanList(l []string) string
//
// Answer classifies every failure in one place: ErrRedirect, ErrTooLarge,
// cancelled or timed out (the context's error), unreadable or unreachable
// (any other error), ErrNotAPIAnswer, or an *APIError.

// The *APIError of an error status is an error, so errors.As finds it.
var _ error = (*contract.APIError)(nil)

const answerLeak = "leak-marker"

// searchAnswer makes one search through the hardened client at base and reads
// it with contract.Answer.
func searchAnswer(ctx context.Context, t *testing.T, base string) (*contract.SearchResults, error) {
	t.Helper()
	c := newAPIClient(t, base, clientToken, nil)
	raw, err := c.SearchArticles(ctx, &contract.SearchArticlesParams{Q: "x"})
	return contract.Answer(raw, err, contract.ParseSearchArticlesResponse,
		func(r *contract.SearchArticlesResponse) *contract.SearchResults { return r.JSON200 })
}

func answerServer(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func reply(status int, header map[string]string, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := header["Content-Type"]; !ok {
			w.Header()["Content-Type"] = nil // no sniffing
		}
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func truncated(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", "1000")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"results":[`))
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

func deadBase(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "http://" + addr
}

// class names what Answer found, so each case asserts one class and that the
// error is in no other.
func class(err error) string {
	var ae *contract.APIError
	var found []string
	if err == nil {
		return "ok"
	}
	if errors.Is(err, contract.ErrRedirect) {
		found = append(found, "redirect")
	}
	if errors.Is(err, contract.ErrTooLarge) {
		found = append(found, "too large")
	}
	if errors.Is(err, context.Canceled) {
		found = append(found, "cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		found = append(found, "timed out")
	}
	if errors.Is(err, contract.ErrNotAPIAnswer) {
		found = append(found, "not an API answer")
	}
	if errors.As(err, &ae) {
		found = append(found, "APIError")
	}
	if len(found) == 0 {
		return "unreadable"
	}
	return strings.Join(found, "+")
}

func TestAnswerClassifiesEveryOutcomeInOnePlace(t *testing.T) {
	jsonType := map[string]string{"Content-Type": "application/json"}
	cases := []struct {
		name  string
		base  func(t *testing.T) string
		ctx   func(t *testing.T) context.Context
		class string
	}{
		{name: "200 JSON", base: func(t *testing.T) string {
			return answerServer(t, reply(http.StatusOK, jsonType, `{"query":"x","results":[]}`))
		}, class: "ok"},
		{name: "200 JSON with a charset", base: func(t *testing.T) string {
			return answerServer(t, reply(http.StatusOK, map[string]string{"Content-Type": "application/json; charset=utf-8"}, `{"query":"x","results":[]}`))
		}, class: "ok"},
		{name: "redirect", base: func(t *testing.T) string {
			return answerServer(t, reply(http.StatusFound, map[string]string{"Location": "http://leak-host.invalid/" + answerLeak}, ""))
		}, class: "redirect"},
		{name: "too large 200", base: func(t *testing.T) string { return sized(t, http.StatusOK, maxAnswer+1) }, class: "too large"},
		{name: "too large 404", base: func(t *testing.T) string { return sized(t, http.StatusNotFound, maxAnswer+1) }, class: "too large"},
		{name: "cancelled", base: func(t *testing.T) string {
			return answerServer(t, reply(http.StatusOK, jsonType, `{"query":"x","results":[]}`))
		}, ctx: func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, class: "cancelled"},
		{name: "timed out", base: func(t *testing.T) string {
			return answerServer(t, func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(10 * time.Second):
				}
			})
		}, ctx: func(t *testing.T) context.Context {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			t.Cleanup(cancel)
			return ctx
		}, class: "timed out"},
		{name: "unreadable", base: func(t *testing.T) string { return answerServer(t, truncated) }, class: "unreadable"},
		{name: "unreachable", base: deadBase, class: "unreadable"},
		{name: "200 text/html login page", base: func(t *testing.T) string {
			return answerServer(t, reply(http.StatusOK, map[string]string{"Content-Type": "text/html"}, "<html>http://10.0.0.7/ "+answerLeak+"</html>"))
		}, class: "not an API answer"},
		{name: "200 without a content type", base: func(t *testing.T) string {
			return answerServer(t, reply(http.StatusOK, nil, answerLeak))
		}, class: "not an API answer"},
		{name: "200 JSON body under text/plain", base: func(t *testing.T) string {
			return answerServer(t, reply(http.StatusOK, map[string]string{"Content-Type": "text/plain"}, `{"query":"x","results":[]}`))
		}, class: "not an API answer"},
		{name: "404 envelope", base: func(t *testing.T) string {
			return answerServer(t, reply(http.StatusNotFound, jsonType, `{"error":{"code":"not_found","message":"no such article","details":{"hint":"search first"}}}`))
		}, class: "APIError"},
		{name: "502 HTML page", base: func(t *testing.T) string {
			return answerServer(t, reply(http.StatusBadGateway, map[string]string{"Content-Type": "text/html"}, "<html>"+answerLeak+"</html>"))
		}, class: "APIError"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			if c.ctx != nil {
				ctx = c.ctx(t)
			}
			base := c.base(t)
			got, err := searchAnswer(ctx, t, base)
			if k := class(err); k != c.class {
				t.Fatalf("Answer: class %q (err %v), want %q", k, err, c.class)
			}
			if c.class == "ok" {
				if got == nil || got.Query != "x" {
					t.Errorf("Answer: body %+v, want the parsed search answer", got)
				}
				return
			}
			if got != nil {
				t.Errorf("Answer: body %+v with an error, want nil", got)
			}
		})
	}
}

// An *APIError carries the status, the cleaned message and hint of the
// envelope, and the wait of a valid Retry-After, exactly as DecodeAPIError
// reads them, and nothing else of the body.
func TestAnswerAPIErrorCarriesWhatDecodeAPIErrorReads(t *testing.T) {
	cases := []struct {
		name string
		h    http.HandlerFunc
		want contract.APIError
	}{
		{"404 envelope", reply(http.StatusNotFound, map[string]string{"Content-Type": "application/json"},
			`{"error":{"code":"not_found","message":"no such\narticle","details":{"hint":"search`+"\u202e"+`first"}}}`),
			contract.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "no such article", Hint: "searchfirst"}},
		{"429 with a wait", reply(http.StatusTooManyRequests, map[string]string{"Retry-After": "30"}, ""),
			contract.APIError{Status: http.StatusTooManyRequests, RetryAfter: 30}},
		{"429 with a wait that is not an integer", reply(http.StatusTooManyRequests, map[string]string{"Retry-After": "soon"}, ""),
			contract.APIError{Status: http.StatusTooManyRequests}},
		{"502 HTML page", reply(http.StatusBadGateway, map[string]string{"Content-Type": "text/html"}, "<html>"+answerLeak+"</html>"),
			contract.APIError{Status: http.StatusBadGateway}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := searchAnswer(context.Background(), t, answerServer(t, c.h))
			var ae *contract.APIError
			if !errors.As(err, &ae) {
				t.Fatalf("Answer: err %v, want an *APIError", err)
			}
			if *ae != c.want {
				t.Errorf("Answer: APIError %+v, want %+v", *ae, c.want)
			}
		})
	}
}

// The errors Answer makes itself carry nothing of the answer or the address:
// they reach the clients' wording, and may be logged.
func TestAnswerErrorsCarryNoAddressOrBody(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"not an API answer": reply(http.StatusOK, map[string]string{"Content-Type": "text/html"}, "<html>http://10.0.0.7/ "+answerLeak+"</html>"),
		"APIError":          reply(http.StatusBadGateway, map[string]string{"Content-Type": "text/html"}, "<html>http://10.0.0.7/ "+answerLeak+"</html>"),
	} {
		base := answerServer(t, h)
		_, err := searchAnswer(context.Background(), t, base)
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		msg := err.Error()
		if msg == "" {
			t.Errorf("%s: empty error text", name)
		}
		for _, bad := range []string{base, strings.TrimPrefix(base, "http://"), "10.0.0.7", answerLeak, clientToken, "<html>"} {
			if strings.Contains(msg, bad) {
				t.Errorf("%s: error %q carries %q", name, msg, bad)
			}
		}
	}
}

// Answer is generic: get_article reads the same way.
func TestAnswerReadsAnArticle(t *testing.T) {
	base := answerServer(t, reply(http.StatusOK, map[string]string{"Content-Type": "application/json"},
		`{"id":"a","name":"A","keywords":["k"],"content":"body"}`))
	c := newAPIClient(t, base, clientToken, nil)
	raw, err := c.GetArticle(context.Background(), "a", nil)
	a, err := contract.Answer(raw, err, contract.ParseGetArticleResponse,
		func(r *contract.GetArticleResponse) *contract.Article { return r.JSON200 })
	if err != nil || a == nil || a.Id != "a" || a.Content != "body" {
		t.Errorf("Answer: article %+v, err %v", a, err)
	}
}

// CleanList joins a list for one line: each entry cleaned with CleanLine,
// joined with ", ", and "(none)" for an empty list.
func TestCleanListJoinsCleanedEntriesForOneLine(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
		want string
	}{
		{"nil", nil, "(none)"},
		{"empty", []string{}, "(none)"},
		{"one", []string{"go"}, "go"},
		{"several", []string{"go", "kubernetes"}, "go, kubernetes"},
		{"forged lines and hidden runes", []string{"a\n- id: forged", "b\u202e\x1b[2J", "c\u2028d"}, "a - id: forged, b[2J, c d"},
	} {
		if got := contract.CleanList(c.in); got != c.want {
			t.Errorf("%s: CleanList(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
