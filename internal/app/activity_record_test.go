package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #242: every search and lookup through the public
// handler is recorded in its Activity, with the token name, the client
// version, the Project and the trace id, and Misses are flagged. The seam is
// the public handler PublicHandler builds; the tests then read its Activity.
// The contract they fix:
//
//	// The Activity the router records into, wired with a bounded capacity.
//	// Public has it by embedding.
//	func (m *publicMux) Activity() *api.Activity
//
//	type api.Event struct {
//		... ID, Replica, Time, Kind, Query as in #241 ...
//		Token         string // the name of the token that authenticated the request, never the secret
//		ClientVersion string // the X-Goldie-Client version as clientVersion reads it, in Semver.String
//		                     // form ("1.5.0" for goldie/v1.5.0); "" when the header is absent or not
//		                     // one goldie/<semver> value
//		Project       string // the project parameter as sent, registered or not; "" without one
//		TraceID       string // platform.TraceID of the request context; "" without a span
//		Miss          bool   // a search with zero results, or a lookup answering 404
//	}
//
// A search's Query is its search words; a lookup's Query is the article id it
// asked for. Until Activity and the new fields land, this package's tests do
// not compile.

// activityHandler builds the public handler over the test content with the
// named tokens (ci-reader, ci-operator) unless cfg sets its own.
func activityHandler(t *testing.T, cfg platform.Config) *Public {
	t.Helper()
	pub, err := PublicHandler(activityConfig(cfg), nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	return pub
}

// activityHandlerKeepalive is activityHandler with the stream keepalive set,
// built the way PublicHandler builds it (parsed tokens, serve source) but
// through newPublic, which takes the keepalive as a parameter.
func activityHandlerKeepalive(t *testing.T, cfg platform.Config, keepalive time.Duration) *Public {
	t.Helper()
	cfg = activityConfig(cfg)
	tokens, err := apiTokens(cfg)
	if err != nil {
		t.Fatalf("apiTokens: %v", err)
	}
	src, err := serveSource(cfg)
	if err != nil {
		t.Fatalf("serveSource: %v", err)
	}
	pub, err := newPublic(cfg, tokens, src, nil, keepalive)
	if err != nil {
		t.Fatalf("newPublic: %v", err)
	}
	return pub
}

// activityConfig fills a test config's content, tokens and rate limit.
func activityConfig(cfg platform.Config) platform.Config {
	if cfg.ContentDir == "" {
		cfg.ContentDir = testContent
	}
	if cfg.APIToken == "" && cfg.APITokens == "" {
		cfg.APITokens = namedTokenLines()
	}
	cfg.RateLimit = withDefaultRateLimit(cfg.RateLimit)
	return cfg
}

// waitEvents returns the events a holds once it holds at least n, failing
// after five seconds. A request is recorded by the time its answer is read
// in process; over a network the event may land just after the answer.
func waitEvents(t *testing.T, a *api.Activity, n int) []api.Event {
	t.Helper()
	if a == nil {
		t.Fatal("the public handler has no Activity")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		history, live, cancel := a.Subscribe()
		cancel()
		if live == nil {
			t.Fatal("the public handler's Activity is disabled: it refused a subscriber")
		}
		if len(history) >= n || time.Now().After(deadline) {
			if len(history) < n {
				t.Fatalf("Activity holds %d events, want at least %d: %+v", len(history), n, history)
			}
			return history
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// lastEvent serves one GET in process with mutate applied and returns the
// event it recorded, after checking the status.
func lastEvent(t *testing.T, pub *Public, path string, status int, mutate func(*http.Request)) api.Event {
	t.Helper()
	if mutate == nil {
		mutate = func(*http.Request) {}
	}
	before := len(waitEvents(t, pub.Activity(), 0))
	rec := serveAs(t, pub, path, mutate)
	if rec.Code != status {
		t.Fatalf("GET %s: status %d, want %d: %s", path, rec.Code, status, rec.Body.String())
	}
	events := waitEvents(t, pub.Activity(), before+1)
	if len(events) != before+1 {
		t.Fatalf("GET %s recorded %d events, want exactly 1: %+v", path, len(events)-before, events[before:])
	}
	return events[before]
}

// assertNoSecret fails when any recorded field carries a token secret.
func assertNoSecret(t *testing.T, events []api.Event) {
	t.Helper()
	all := fmt.Sprintf("%+v", events)
	for _, secret := range []string{readerToken, operatorToken, hashedSecret, testToken, "Bearer"} {
		if strings.Contains(all, secret) {
			t.Errorf("a recorded event carries %q: %s", secret, all)
		}
	}
}

func withClient(client string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(clientHeader, client) }
}

// Criterion: a search is recorded with the token name, the client version,
// the Project and its search words, once.
func TestSearchIsRecordedWithTokenClientVersionAndProject(t *testing.T) {
	pub := activityHandler(t, platform.Config{})
	e := lastEvent(t, pub, "/api/v1/search?q=kubernetes&project=github.com/acme/shop", http.StatusOK, withClient("goldie/v1.5.0"))
	if e.Kind != api.KindSearch || e.Query != "kubernetes" {
		t.Errorf("event kind %q query %q, want search \"kubernetes\"", e.Kind, e.Query)
	}
	if e.Token != "ci-reader" {
		t.Errorf("event token %q, want the token's name ci-reader", e.Token)
	}
	if e.ClientVersion != "1.5.0" {
		t.Errorf("event client version %q, want 1.5.0 from X-Goldie-Client: goldie/v1.5.0", e.ClientVersion)
	}
	if e.Project != "github.com/acme/shop" {
		t.Errorf("event project %q, want github.com/acme/shop", e.Project)
	}
	if e.Miss {
		t.Error("a search with results is recorded as a Miss")
	}
	if e.TraceID != "" {
		t.Errorf("event trace id %q without telemetry, want \"\"", e.TraceID)
	}
	assertNoSecret(t, waitEvents(t, pub.Activity(), 1))
}

// Criterion: a lookup is recorded the same way, with the id it asked for and
// the name of the token that sent it.
func TestLookupIsRecordedWithTokenClientVersionAndProject(t *testing.T) {
	pub := activityHandler(t, platform.Config{})
	e := lastEvent(t, pub, "/api/v1/articles/platform%2Fdeploy?project=github.com/acme/shop", http.StatusOK,
		asClient(operatorToken, "goldie/2.0.0-rc.1"))
	if e.Kind != api.KindLookup || e.Query != "platform/deploy" {
		t.Errorf("event kind %q query %q, want lookup \"platform/deploy\"", e.Kind, e.Query)
	}
	if e.Token != "ci-operator" || e.ClientVersion != "2.0.0-rc.1" || e.Project != "github.com/acme/shop" {
		t.Errorf("event token %q client %q project %q, want ci-operator, 2.0.0-rc.1, github.com/acme/shop", e.Token, e.ClientVersion, e.Project)
	}
	if e.Miss {
		t.Error("a lookup answering 200 is recorded as a Miss")
	}
	assertNoSecret(t, waitEvents(t, pub.Activity(), 1))
}

// The client version is X-Goldie-Client read as the version gate reads it:
// exactly one goldie/<semver> value of at most 256 bytes. Anything else is no
// version, and its text is not stored.
func TestClientVersionIsRecordedOnlyForOneGoldieVersion(t *testing.T) {
	pub := activityHandler(t, platform.Config{})
	cases := []struct {
		name   string
		values []string
		want   string
	}{
		{"none", nil, ""},
		{"release", []string{"goldie/1.5.0"}, "1.5.0"},
		{"release tag", []string{"goldie/v1.6.0"}, "1.6.0"},
		{"prerelease", []string{"goldie/1.7.0-beta.2"}, "1.7.0-beta.2"},
		{"other client", []string{"curl/8.4.0"}, ""},
		{"not semver", []string{"goldie/banana-planted"}, ""},
		{"two values", []string{"goldie/1.5.0", "goldie/1.6.0"}, ""},
		{"over 256 bytes", []string{"goldie/1.5.0-" + strings.Repeat("p", 300)}, ""},
	}
	for _, c := range cases {
		e := lastEvent(t, pub, "/api/v1/search?q=kubernetes", http.StatusOK, func(r *http.Request) {
			for _, v := range c.values {
				r.Header.Add(clientHeader, v)
			}
		})
		if e.ClientVersion != c.want {
			t.Errorf("%s: client version %q, want %q", c.name, e.ClientVersion, c.want)
		}
	}
	all := fmt.Sprintf("%+v", waitEvents(t, pub.Activity(), len(cases)))
	for _, leak := range []string{"curl", "banana", strings.Repeat("p", 20)} {
		if strings.Contains(all, leak) {
			t.Errorf("an unreadable X-Goldie-Client value is stored (%q): %s", leak, all)
		}
	}
}

// Criterion: a search with zero results is a Miss; one with results is not.
func TestZeroResultSearchIsAMiss(t *testing.T) {
	pub := activityHandler(t, platform.Config{})
	e := lastEvent(t, pub, "/api/v1/search?q=cobol", http.StatusOK, nil)
	if e.Kind != api.KindSearch || e.Query != "cobol" || !e.Miss {
		t.Errorf("zero-result search recorded as kind %q query %q miss %v, want search \"cobol\" and a Miss", e.Kind, e.Query, e.Miss)
	}
	if e.Project != "" {
		t.Errorf("a search without a project recorded project %q", e.Project)
	}
	if e = lastEvent(t, pub, "/api/v1/search?q=kubernetes", http.StatusOK, nil); e.Miss {
		t.Error("a search with results is recorded as a Miss")
	}
}

// Criterion: a lookup answering 404 is a Miss, recorded with the id it asked
// for, nested ids included.
func TestLookupAnswering404IsAMiss(t *testing.T) {
	pub := activityHandler(t, platform.Config{})
	for path, id := range map[string]string{
		"/api/v1/articles/no-such-article":                                 "no-such-article",
		"/api/v1/articles/platform%2Fnope":                                 "platform/nope",
		"/api/v1/articles/no-such-article?project=github.com/acme/shop":    "no-such-article",
		"/api/v1/articles/platform%2Fmissing?project=github.com/acme/none": "platform/missing",
	} {
		e := lastEvent(t, pub, path, http.StatusNotFound, nil)
		if e.Kind != api.KindLookup || e.Query != id || !e.Miss {
			t.Errorf("%s: recorded kind %q query %q miss %v, want lookup %q and a Miss", path, e.Kind, e.Query, e.Miss, id)
		}
	}
}

// Criterion: a request naming an unregistered Project is recorded with that
// id, though it got the global answer.
func TestUnregisteredProjectIsRecordedWithItsID(t *testing.T) {
	pub := activityHandler(t, platform.Config{})
	for _, path := range []string{
		"/api/v1/search?q=kubernetes&project=github.com/acme/none",
		"/api/v1/articles/platform%2Fdeploy?project=github.com/acme/none",
		"/api/v1/search?q=cobol&project=gitlab.example.org/team-a/svc_1.x",
	} {
		e := lastEvent(t, pub, path, http.StatusOK, nil)
		want := path[strings.Index(path, "project=")+len("project="):]
		if e.Project != want {
			t.Errorf("%s: recorded project %q, want the unregistered id %q", path, e.Project, want)
		}
	}
}

// Criterion: a search through MCP is recorded with the MCP client version.
// goldie mcp over stdio reaches the API with its own X-Goldie-Client, so its
// searches and lookups carry the goldie mcp version and the token it sends.
func TestMCPSearchIsRecordedWithTheMCPClientVersion(t *testing.T) {
	setClientVersion(t, "1.7.2")
	pub := activityHandler(t, platform.Config{APIToken: testToken})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	cs := stdioSession(t, srv.URL)
	if text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "agents loops"}); isErr {
		t.Fatalf("search_knowledge: %s", text)
	}
	events := waitEvents(t, pub.Activity(), 1)
	if len(events) != 1 {
		t.Fatalf("one MCP search recorded %d events, want 1: %+v", len(events), events)
	}
	e := events[0]
	if e.Kind != api.KindSearch || e.Query != "agents loops" || e.Token != api.ShorthandName || e.ClientVersion != "1.7.2" {
		t.Errorf("MCP search recorded as kind %q query %q token %q client %q; want search \"agents loops\", %s, 1.7.2",
			e.Kind, e.Query, e.Token, e.ClientVersion, api.ShorthandName)
	}
	if text, isErr := call(t, cs, "get_article", map[string]any{"id": "agent-loops"}); isErr {
		t.Fatalf("get_article: %s", text)
	}
	if e = waitEvents(t, pub.Activity(), 2)[1]; e.Kind != api.KindLookup || e.Query != "agent-loops" || e.ClientVersion != "1.7.2" {
		t.Errorf("MCP lookup recorded as kind %q query %q client %q; want lookup \"agent-loops\", 1.7.2", e.Kind, e.Query, e.ClientVersion)
	}
}

// Criterion: each search is recorded with the token name, through /mcp too:
// the tool call's API request carries the caller's token, and the POST /mcp
// around it is not an event of its own.
func TestMCPOverHTTPSearchIsRecordedWithTheCallersToken(t *testing.T) {
	pub := activityHandler(t, platform.Config{})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(readerToken, http.DefaultTransport))
	if text, isErr := call(t, cs, "search_knowledge", map[string]any{"query": "cobol"}); isErr {
		t.Fatalf("search_knowledge: %s", text)
	}
	events := waitEvents(t, pub.Activity(), 1)
	if len(events) != 1 {
		t.Fatalf("one MCP search recorded %d events, want 1: %+v", len(events), events)
	}
	if e := events[0]; e.Kind != api.KindSearch || e.Query != "cobol" || e.Token != "ci-reader" || !e.Miss {
		t.Errorf("MCP search recorded as kind %q query %q token %q miss %v; want search \"cobol\", ci-reader, a Miss", e.Kind, e.Query, e.Token, e.Miss)
	}
	assertNoSecret(t, events)
}

// rejected is one request a guard or a check refuses; every planted value in
// it carries "planted".
type rejected struct {
	path   string
	status int
	mutate func(*http.Request)
}

// assertRecordsNoPlantedText serves every request, checks its status, then
// serves sentinel (a request that is recorded, or "" for none) and fails when
// any event the handler recorded carries "planted".
func assertRecordsNoPlantedText(t *testing.T, h http.Handler, a func() *api.Activity, reqs []rejected, sentinel string) {
	t.Helper()
	for _, r := range reqs {
		rec := httptest.NewRequestWithContext(t.Context(), http.MethodGet, r.path, nil)
		if r.mutate != nil {
			r.mutate(rec)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, rec)
		if w.Code != r.status {
			t.Fatalf("fixture: GET %s: status %d, want %d: %s", r.path, w.Code, r.status, w.Body.String())
		}
	}
	n := 0
	if sentinel != "" {
		events := waitEvents(t, a(), 0)
		if w := serveAs(t, h, sentinel, func(*http.Request) {}); w.Code != http.StatusOK {
			t.Fatalf("fixture: sentinel %s: status %d", sentinel, w.Code)
		}
		n = len(events) + 1
	}
	events := waitEvents(t, a(), n)
	if all := fmt.Sprintf("%+v", events); strings.Contains(all, "planted") {
		t.Errorf("a rejected request's query text or id is recorded: %s", all)
	}
}

// Criterion: rejected requests (400, 401, 426, 429, and 503 not_ready by the
// PM decision) record no query text, and no id: neither the search words, the
// article id nor a project that was refused.
func TestRejectedRequestsRecordNoQueryText(t *testing.T) {
	t.Run("400", func(t *testing.T) {
		pub := activityHandler(t, platform.Config{})
		reader := bearer(readerToken)
		assertRecordsNoPlantedText(t, pub, pub.Activity, []rejected{
			{"/api/v1/search?q=planted" + strings.Repeat("a", 1025), http.StatusBadRequest, reader},
			{"/api/v1/search?q=plantedwords&project=acme/shop", http.StatusBadRequest, reader},
			{"/api/v1/search?q=plantedwords&project=github.com/acme/planted%0AInjected", http.StatusBadRequest, reader},
			{"/api/v1/search?q=plantedone&q=plantedtwo", http.StatusBadRequest, reader},
			{"/api/v1/articles/plantedid?project=github.com/acme/%3Cplanted%3E", http.StatusBadRequest, reader},
			{"/api/v1/articles/platform%2Fdeploy?project=planted/shop", http.StatusBadRequest, reader},
		}, "/api/v1/search?q=kubernetes")
	})
	t.Run("401", func(t *testing.T) {
		pub := activityHandler(t, platform.Config{})
		assertRecordsNoPlantedText(t, pub, pub.Activity, []rejected{
			{"/api/v1/search?q=plantedwords", http.StatusUnauthorized, nil},
			{"/api/v1/search?q=plantedwords&project=github.com/acme/planted", http.StatusUnauthorized, bearer("wrong-token-planted-0123456789abcdef")},
			{"/api/v1/articles/plantedid", http.StatusUnauthorized, nil},
		}, "/api/v1/search?q=kubernetes")
	})
	t.Run("426", func(t *testing.T) {
		pub := activityHandler(t, platform.Config{MinClientVersion: semver(t, minVersion)})
		old := asClient(readerToken, "goldie/1.3.0")
		assertRecordsNoPlantedText(t, pub, pub.Activity, []rejected{
			{"/api/v1/search?q=plantedwords&project=github.com/acme/planted", http.StatusUpgradeRequired, old},
			{"/api/v1/articles/plantedid", http.StatusUpgradeRequired, old},
		}, "/api/v1/search?q=kubernetes")
	})
	t.Run("429", func(t *testing.T) {
		pub := activityHandler(t, platform.Config{RateLimit: platform.RateLimit{Requests: 1, Per: time.Minute, Burst: 1}})
		reader := bearer(readerToken)
		assertRecordsNoPlantedText(t, pub, pub.Activity, []rejected{
			{"/api/v1/search?q=kubernetes", http.StatusOK, reader},
			{"/api/v1/search?q=plantedwords&project=github.com/acme/planted", http.StatusTooManyRequests, reader},
			{"/api/v1/articles/plantedid", http.StatusTooManyRequests, reader},
		}, "")
		if events := waitEvents(t, pub.Activity(), 1); events[0].Query != "kubernetes" {
			t.Errorf("the served search before the limit is not recorded: %+v", events)
		}
	})
	t.Run("503 not_ready", func(t *testing.T) {
		tokens, err := api.ParseTokens(namedTokenLines())
		if err != nil {
			t.Fatal(err)
		}
		m, err := newPublicMuxKeepalive(func() *engine.Snapshot { return nil }, tokens, rateLimiter(platform.DefaultRateLimit()), nil, api.DefaultActivityKeepalive)
		if err != nil {
			t.Fatal(err)
		}
		reader := bearer(readerToken)
		assertRecordsNoPlantedText(t, m, m.Activity, []rejected{
			{"/api/v1/search?q=plantedwords", http.StatusServiceUnavailable, reader},
			{"/api/v1/search?q=plantedwords&project=github.com/acme/planted", http.StatusServiceUnavailable, reader},
			{"/api/v1/articles/plantedid", http.StatusServiceUnavailable, reader},
			{"/api/v1/articles/platform%2Fplanted?project=github.com/acme/planted", http.StatusServiceUnavailable, reader},
		}, "")
	})
}
