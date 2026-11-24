package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/api/github"
)

const (
	testCode     = "code-4f1a9c"
	testVerifier = "verifier-7d2e0b-0123456789-0123456789-0123456789"
	testRedirect = "https://goldie.example/api/v1/session/github/callback"
	testToken    = "gho_ACCESSTOKEN0123456789"
	testSecret   = "client-secret-9e8d7c"
	testBodyLeak = "UPSTREAM-BODY-LEAK-31337"
)

// fakeGitHub is a GitHub Enterprise server: the OAuth endpoint under the base
// URL, the REST API under /api/v3.
type fakeGitHub struct {
	*httptest.Server
	mu sync.Mutex
	// login is what /api/v3/user answers for testToken.
	login string
	// orgs maps org to the state of login's membership ("active", "pending").
	orgs map[string]string
	// teams maps "org/slug" to the state of login's membership.
	teams map[string]string
	// override answers a path (exact match) with a status and body instead.
	override map[string]fakeAnswer
	// tokenForm is the last form posted to the token endpoint.
	tokenForm url.Values
	// paths records every request path, so tests can see what was asked.
	paths []string
	// tokenCalls counts token exchanges.
	tokenCalls int
}

type fakeAnswer struct {
	status int
	body   string
}

func newFake(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{login: "alice", orgs: map[string]string{}, teams: map[string]string{}, override: map[string]fakeAnswer{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	ans, over := f.override[r.URL.Path]
	f.mu.Unlock()
	if over {
		w.WriteHeader(ans.status)
		_, _ = io.WriteString(w, ans.body)
		return
	}
	switch {
	case r.URL.Path == "/login/oauth/access_token":
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		f.mu.Lock()
		f.tokenForm = r.PostForm
		f.tokenCalls++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("code") != testCode || r.PostForm.Get("code_verifier") != testVerifier ||
			r.PostForm.Get("redirect_uri") != testRedirect {
			_, _ = io.WriteString(w, `{"error":"bad_verification_code","error_description":"`+testBodyLeak+`"}`)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"`+testToken+`","token_type":"bearer","scope":"read:org"}`)
	case !strings.HasPrefix(r.URL.Path, "/api/v3/"):
		http.NotFound(w, r)
	case r.Header.Get("Authorization") != "Bearer "+testToken && r.Header.Get("Authorization") != "token "+testToken:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"`+testBodyLeak+`"}`)
	case r.URL.Path == "/api/v3/user":
		f.mu.Lock()
		login := f.login
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"login": login})
	default:
		f.membership(w, r)
	}
}

// membership answers the org and team membership endpoints. Both documented
// spellings of the org check are served, so the tests pin the behaviour and
// not the choice of endpoint.
func (f *fakeGitHub) membership(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v3")
	var state string
	var ok bool
	parts := strings.Split(strings.Trim(p, "/"), "/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case len(parts) == 4 && parts[0] == "orgs" && parts[2] == "memberships" && parts[3] == f.login:
		state, ok = f.orgs[parts[1]]
	case len(parts) == 4 && parts[0] == "user" && parts[1] == "memberships" && parts[2] == "orgs":
		state, ok = f.orgs[parts[3]]
	case len(parts) == 6 && parts[0] == "orgs" && parts[2] == "teams" && parts[4] == "memberships" && parts[5] == f.login:
		state, ok = f.teams[parts[1]+"/"+parts[3]]
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"state": state, "role": "member"})
}

func (f *fakeGitHub) client(t *testing.T, mutate func(*github.Options)) *github.Client {
	t.Helper()
	opts := github.Options{
		BaseURL:       f.URL,
		ClientID:      "goldie-client",
		ClientSecret:  testSecret,
		Orgs:          []string{"acme"},
		OperatorTeams: []string{"acme/platform"},
		Timeout:       2 * time.Second,
	}
	if mutate != nil {
		mutate(&opts)
	}
	c, err := github.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func (f *fakeGitHub) requested(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.paths {
		if p == path {
			return true
		}
	}
	return false
}

func (f *fakeGitHub) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls
}

func exchange(c *github.Client) (github.Identity, error) {
	return c.Exchange(context.Background(), testCode, testVerifier, testRedirect)
}

// secretsIn lists every secret or upstream text found in s.
func secretsIn(s string) []string {
	var found []string
	for _, bad := range []string{testToken, testSecret, testBodyLeak, testCode, testVerifier} {
		if strings.Contains(s, bad) {
			found = append(found, bad)
		}
	}
	return found
}

func TestExchangeYieldsLoginAndReaderRoleForOrgMember(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	c := f.client(t, nil)

	id, err := exchange(c)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Login != "alice" || id.Role != github.RoleReader {
		t.Fatalf("identity = %+v, want alice as reader", id)
	}
	if got := github.RoleReader.String(); got != "reader" {
		t.Errorf("RoleReader.String() = %q, want reader", got)
	}
}

func TestExchangeSendsCodeVerifierAndRedirectURIToGitHub(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	if _, err := exchange(f.client(t, nil)); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, want := range map[string]string{
		"code": testCode, "code_verifier": testVerifier, "redirect_uri": testRedirect,
		"client_id": "goldie-client", "client_secret": testSecret,
	} {
		if got := f.tokenForm.Get(k); got != want {
			t.Errorf("token request %s = %q, want %q", k, got, want)
		}
	}
}

func TestExchangeNeverReturnsOrKeepsTheGitHubToken(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	c := f.client(t, nil)

	id, err := exchange(c)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	for _, v := range []any{id, &id, c} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			if got := secretsIn(fmt.Sprintf(format, v)); len(got) > 0 {
				t.Errorf("%T printed with %s leaks %v", v, format, got)
			}
		}
	}
	// The Identity carries a login and a Role and nothing else: no field can
	// hold a token.
	raw, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("identity is not a JSON object: %v", err)
	}
	if len(fields) > 2 {
		t.Errorf("Identity has %d fields (%s), want only a login and a Role", len(fields), raw)
	}

	// A second exchange with the same client asks GitHub again: nothing is
	// cached, so the token was not kept.
	if _, err := exchange(c); err != nil {
		t.Fatalf("second Exchange: %v", err)
	}
	if n := f.calls(); n != 2 {
		t.Errorf("token exchanges = %d, want 2 (one per Exchange)", n)
	}
}

func TestExchangeActiveOperatorTeamMemberIsOperator(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	f.teams["acme/platform"] = "active"

	id, err := exchange(f.client(t, nil))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Login != "alice" || id.Role != github.RoleOperator {
		t.Fatalf("identity = %+v, want alice as operator", id)
	}
	if got := github.RoleOperator.String(); got != "operator" {
		t.Errorf("RoleOperator.String() = %q, want operator", got)
	}
}

func TestExchangeOperatorTeamMemberOutsideListedOrgsIsStillOperator(t *testing.T) {
	f := newFake(t)
	f.teams["acme/platform"] = "active"

	id, err := exchange(f.client(t, nil))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Role != github.RoleOperator {
		t.Fatalf("role = %v, want operator", id.Role)
	}
}

func TestExchangeOrgMemberNotOnTheTeamStaysReader(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	// another team, not the listed one
	f.teams["acme/docs"] = "active"

	id, err := exchange(f.client(t, nil))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Role != github.RoleReader {
		t.Fatalf("role = %v, want reader", id.Role)
	}
}

func TestExchangeAnyListedOrgGrantsReader(t *testing.T) {
	f := newFake(t)
	f.orgs["globex"] = "active"

	id, err := exchange(f.client(t, func(o *github.Options) { o.Orgs = []string{"acme", "globex"} }))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Role != github.RoleReader {
		t.Fatalf("role = %v, want reader", id.Role)
	}
}

func TestExchangeNonMemberGetsTypedNotMemberError(t *testing.T) {
	f := newFake(t)
	// alice is in an org that is not listed, and on a team that is not listed.
	f.orgs["other"] = "active"
	f.teams["other/platform"] = "active"

	id, err := exchange(f.client(t, nil))
	if !errors.Is(err, github.ErrNotMember) {
		t.Fatalf("err = %v, want github.ErrNotMember", err)
	}
	if id != (github.Identity{}) {
		t.Errorf("identity = %+v, want none for a non-member", id)
	}
}

func TestExchangePendingMembershipIsRejectedNeverARole(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "pending"
	f.teams["acme/platform"] = "pending"

	id, err := exchange(f.client(t, nil))
	if err == nil {
		t.Fatalf("Exchange with only pending memberships = %+v, want an error", id)
	}
	if !errors.Is(err, github.ErrNotMember) {
		t.Errorf("err = %v, want github.ErrNotMember", err)
	}
	if id != (github.Identity{}) {
		t.Errorf("identity = %+v, want none", id)
	}
}

func TestExchangePendingTeamDoesNotElevateAnActiveOrgMember(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	f.teams["acme/platform"] = "pending"

	id, err := exchange(f.client(t, nil))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Role != github.RoleReader {
		t.Fatalf("role = %v, want reader: a pending team membership is not operator", id.Role)
	}
}

func TestExchangeMembershipNotFoundIsRejected(t *testing.T) {
	f := newFake(t) // alice is in no org: GitHub answers 404 on every membership
	id, err := exchange(f.client(t, nil))
	if !errors.Is(err, github.ErrNotMember) {
		t.Fatalf("err = %v, want github.ErrNotMember", err)
	}
	if id != (github.Identity{}) {
		t.Errorf("identity = %+v, want none", id)
	}
}

func TestExchangeNon2xxFromGitHubIsARejectionNeverARole(t *testing.T) {
	endpoints := map[string]string{
		"token endpoint": "/login/oauth/access_token",
		"user":           "/api/v3/user",
		"org membership": "/api/v3/orgs/acme/memberships/alice",
		"user org check": "/api/v3/user/memberships/orgs/acme",
		"team":           "/api/v3/orgs/acme/teams/platform/memberships/alice",
	}
	for name, path := range endpoints {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusBadGateway, http.StatusFound} {
			t.Run(fmt.Sprintf("%s %d", name, status), func(t *testing.T) {
				f := newFake(t)
				f.orgs["acme"] = "active"
				f.teams["acme/platform"] = "active"
				// A body that, if believed, would grant the Role.
				f.override[path] = fakeAnswer{status: status, body: `{"login":"alice","state":"active","access_token":"` + testToken + `","message":"` + testBodyLeak + `"}`}

				id, err := exchange(f.client(t, nil))
				if !f.requested(path) {
					t.Skipf("the implementation does not use %s", path)
				}
				if err == nil {
					t.Fatalf("Exchange with %d from %s = %+v, want an error", status, path, id)
				}
				if id != (github.Identity{}) {
					t.Errorf("identity = %+v, want none", id)
				}
			})
		}
	}
}

func TestExchangeTokenEndpointErrorWith200IsARejection(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	// GitHub answers a bad or reused code with 200 and an "error" field.
	id, err := f.client(t, nil).Exchange(context.Background(), "wrong-code", testVerifier, testRedirect)
	if err == nil {
		t.Fatalf("Exchange with a bad code = %+v, want an error", id)
	}
	if id != (github.Identity{}) {
		t.Errorf("identity = %+v, want none", id)
	}
	if f.requested("/api/v3/user") {
		t.Error("GitHub's API was called although the code exchange failed")
	}
	if got := secretsIn(err.Error()); len(got) > 0 {
		t.Errorf("error %q leaks %v", err, got)
	}
}

func TestExchangeRejectsAMalformedOrEmptyTokenAnswer(t *testing.T) {
	for name, body := range map[string]string{
		"empty object": `{}`,
		"empty token":  `{"access_token":""}`,
		"not json":     `<html>` + testBodyLeak + `</html>`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.orgs["acme"] = "active"
			f.override["/login/oauth/access_token"] = fakeAnswer{status: http.StatusOK, body: body}
			id, err := exchange(f.client(t, nil))
			if err == nil {
				t.Fatalf("Exchange = %+v, want an error", id)
			}
			if got := secretsIn(err.Error()); len(got) > 0 {
				t.Errorf("error %q leaks %v", err, got)
			}
		})
	}
}

func TestExchangeRejectsALoginThatCouldRedirectTheMembershipCall(t *testing.T) {
	for _, login := range []string{"", "../../admin", "alice/../x", "alice?x=1", "ali ce", "alice#", "al\u202eice"} {
		t.Run(fmt.Sprintf("%q", login), func(t *testing.T) {
			f := newFake(t)
			f.login = login
			f.orgs["acme"] = "active"
			f.teams["acme/platform"] = "active"
			id, err := exchange(f.client(t, nil))
			if err == nil {
				t.Fatalf("Exchange with login %q = %+v, want an error", login, id)
			}
			if id != (github.Identity{}) {
				t.Errorf("identity = %+v, want none", id)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, p := range f.paths {
				if strings.Contains(p, "admin") || strings.Contains(p, "..") {
					t.Errorf("request path %q was built from the hostile login", p)
				}
			}
		})
	}
}

func TestExchangeHonoursAnEnterpriseBaseURL(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	f.teams["acme/platform"] = "active"

	// With and without a trailing slash, the API lives under /api/v3 and
	// OAuth under the base URL itself.
	for _, base := range []string{f.URL, f.URL + "/"} {
		f.mu.Lock()
		f.paths = nil
		f.mu.Unlock()
		c := f.client(t, func(o *github.Options) { o.BaseURL = base })
		id, err := exchange(c)
		if err != nil {
			t.Fatalf("Exchange with base %q: %v", base, err)
		}
		if id.Role != github.RoleOperator {
			t.Errorf("base %q: role = %v, want operator", base, id.Role)
		}
		if !f.requested("/api/v3/user") {
			t.Errorf("base %q: /api/v3/user was not called", base)
		}
		f.mu.Lock()
		for _, p := range f.paths {
			if p != "/login/oauth/access_token" && !strings.HasPrefix(p, "/api/v3/") {
				t.Errorf("base %q: call to %q, want OAuth at /login/oauth/access_token and the API under /api/v3", base, p)
			}
		}
		f.mu.Unlock()
	}
}

func TestNewRejectsAnUnusableBaseURL(t *testing.T) {
	for _, base := range []string{"ftp://ghe.example", "file:///etc", "://", "https://user:pw@ghe.example", "javascript:alert(1)"} {
		_, err := github.New(github.Options{BaseURL: base, ClientID: "id", ClientSecret: testSecret, Orgs: []string{"acme"}})
		if err == nil {
			t.Errorf("New with base URL %q succeeded, want an error", base)
			continue
		}
		if strings.Contains(err.Error(), "pw@") || strings.Contains(err.Error(), "pw:") {
			t.Errorf("error %q leaks the URL's password", err)
		}
	}
}

func TestExchangeErrorsNeverContainGitHubBodiesOrSecrets(t *testing.T) {
	leaky := `{"message":"` + testBodyLeak + `","access_token":"` + testToken + `","login":"` + testBodyLeak + `","state":"` + testBodyLeak + `","error":"` + testBodyLeak + `"}`
	paths := []string{
		"/login/oauth/access_token", "/api/v3/user",
		"/api/v3/orgs/acme/memberships/alice", "/api/v3/user/memberships/orgs/acme",
		"/api/v3/orgs/acme/teams/platform/memberships/alice",
	}
	for _, path := range paths {
		for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("%s %d", path, status), func(t *testing.T) {
				f := newFake(t)
				f.orgs["acme"] = "active"
				f.override[path] = fakeAnswer{status: status, body: leaky}
				_, err := exchange(f.client(t, nil))
				if err == nil {
					return // a 200 on a path that is not needed
				}
				if got := secretsIn(err.Error()); len(got) > 0 {
					t.Errorf("error %q leaks %v", err, got)
				}
			})
		}
	}

	// A transport failure must not echo the request URL's query or form.
	f := newFake(t)
	c := f.client(t, nil)
	f.Close()
	_, err := exchange(c)
	if err == nil {
		t.Fatal("Exchange against a closed server succeeded")
	}
	if got := secretsIn(err.Error()); len(got) > 0 {
		t.Errorf("transport error %q leaks %v", err, got)
	}
}

func TestExchangeCapsTheSizeOfGitHubAnswers(t *testing.T) {
	// A valid answer padded far past any sane size: an uncapped decoder
	// would accept it and return a Role.
	pad := strings.Repeat("a", 32<<20)
	for name, path := range map[string]string{
		"token":      "/login/oauth/access_token",
		"user":       "/api/v3/user",
		"membership": "/api/v3/orgs/acme/memberships/alice",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.orgs["acme"] = "active"
			body := map[string]string{
				"/login/oauth/access_token":           `{"access_token":"` + testToken + `","pad":"` + pad + `"}`,
				"/api/v3/user":                        `{"login":"alice","pad":"` + pad + `"}`,
				"/api/v3/orgs/acme/memberships/alice": `{"state":"active","pad":"` + pad + `"}`,
			}[path]
			f.override[path] = fakeAnswer{status: http.StatusOK, body: body}
			if name == "membership" {
				// The other spelling of the org check, so either endpoint is covered.
				f.override["/api/v3/user/memberships/orgs/acme"] = fakeAnswer{status: http.StatusOK, body: `{"state":"active","pad":"` + pad + `"}`}
			}

			id, err := exchange(f.client(t, nil))
			if err == nil {
				t.Fatalf("Exchange with an oversized %s answer = %+v, want an error", name, id)
			}
			if id != (github.Identity{}) {
				t.Errorf("identity = %+v, want none", id)
			}
		})
	}
}

func TestExchangeTimesOutOnAStalledGitHub(t *testing.T) {
	for name, path := range map[string]string{
		"token": "/login/oauth/access_token",
		"user":  "/api/v3/user",
	} {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == path {
					select {
					case <-release:
					case <-r.Context().Done():
					case <-time.After(10 * time.Second):
					}
					return
				}
				switch r.URL.Path {
				case "/login/oauth/access_token":
					_, _ = io.WriteString(w, `{"access_token":"`+testToken+`"}`)
				default:
					_, _ = io.WriteString(w, `{"login":"alice","state":"active"}`)
				}
			}))
			defer stalled.Close()
			defer close(release)

			c, err := github.New(github.Options{
				BaseURL: stalled.URL, ClientID: "id", ClientSecret: testSecret,
				Orgs: []string{"acme"}, Timeout: 200 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			start := time.Now()
			_, err = exchange(c)
			if err == nil {
				t.Fatal("Exchange against a stalled GitHub succeeded")
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Errorf("Exchange took %v, want it cut off near the 200ms timeout", elapsed)
			}
		})
	}
}

func TestExchangeHonoursACancelledContext(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id, err := f.client(t, nil).Exchange(ctx, testCode, testVerifier, testRedirect)
	if err == nil {
		t.Fatalf("Exchange with a cancelled context = %+v, want an error", id)
	}
}

func TestExchangeDefaultTimeoutIsSetWhenNoneIsConfigured(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	c := f.client(t, func(o *github.Options) { o.Timeout = 0 })
	// The zero Timeout must not mean "wait forever"; the call still works.
	if _, err := exchange(c); err != nil {
		t.Fatalf("Exchange with the default timeout: %v", err)
	}
}

func TestExchangeRequiresCodeVerifierAndRedirectURI(t *testing.T) {
	f := newFake(t)
	f.orgs["acme"] = "active"
	c := f.client(t, nil)
	for name, args := range map[string][3]string{
		"no code":     {"", testVerifier, testRedirect},
		"no verifier": {testCode, "", testRedirect},
		"no redirect": {testCode, testVerifier, ""},
	} {
		t.Run(name, func(t *testing.T) {
			id, err := c.Exchange(context.Background(), args[0], args[1], args[2])
			if err == nil {
				t.Fatalf("Exchange(%q) = %+v, want an error", name, id)
			}
		})
	}
	if n := f.calls(); n != 0 {
		t.Errorf("GitHub's token endpoint was called %d times with incomplete input, want 0", n)
	}
}
