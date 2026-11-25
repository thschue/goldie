package github_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/thschue/goldie/internal/api/github"
)

// revokeFake records the revocation call GitHub receives after a sign-in.
type revokeFake struct {
	mu     sync.Mutex
	calls  int
	path   string
	user   string
	secret string
	body   map[string]string
	member bool
}

func (f *revokeFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/login/oauth/access_token":
		_, _ = io.WriteString(w, `{"access_token":"`+testToken+`"}`)
	case r.URL.Path == "/api/v3/user":
		_, _ = io.WriteString(w, `{"login":"alice"}`)
	case r.URL.Path == "/api/v3/user/memberships/orgs/acme":
		if !f.member {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"state":"active"}`)
	case r.Method == http.MethodDelete:
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		f.path = r.URL.Path
		f.user, f.secret, _ = r.BasicAuth()
		_ = json.NewDecoder(r.Body).Decode(&f.body)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func revokeClient(t *testing.T, member bool) (*github.Client, *revokeFake) {
	t.Helper()
	f := &revokeFake{member: member}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := github.New(github.Options{BaseURL: srv.URL, ClientID: "Iv1.app", ClientSecret: testSecret, Orgs: []string{"acme"}})
	if err != nil {
		t.Fatal(err)
	}
	return c, f
}

// The access token is deleted at GitHub once the Role is mapped, as the OAuth
// app, so it never outlives the Exchange (#644 criterion 9).
func TestExchangeRevokesTheAccessTokenAfterMappingTheRole(t *testing.T) {
	c, f := revokeClient(t, true)
	if _, err := c.Exchange(context.Background(), testCode, testVerifier, testRedirect); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls != 1 || f.path != "/api/v3/applications/Iv1.app/token" {
		t.Fatalf("revocations = %d at %q, want one at /api/v3/applications/Iv1.app/token", f.calls, f.path)
	}
	if f.user != "Iv1.app" || f.secret != testSecret || f.body["access_token"] != testToken {
		t.Fatalf("revocation not made as the app with the token: user %q, body %v", f.user, f.body)
	}
}

// A non-member's token is revoked too: every Exchange that got a token ends it.
func TestExchangeRevokesTheAccessTokenOfANonMember(t *testing.T) {
	c, f := revokeClient(t, false)
	if _, err := c.Exchange(context.Background(), testCode, testVerifier, testRedirect); err == nil {
		t.Fatal("a non-member signed in")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls != 1 {
		t.Fatalf("revocations = %d, want 1", f.calls)
	}
}
