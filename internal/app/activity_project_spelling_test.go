package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #243, from #325: each Event stores the registered
// spelling of its Project, and an unregistered id lower-cased, so one
// Project's Misses never split on case (ADR 0020). Project ids match
// case-insensitively; the fixture registers github.com/Acme/Shop, whose
// spelling differs from the lower-cased id clients send.

const (
	spellingContent    = "testdata/activity-content"
	registeredSpelling = "github.com/Acme/Shop"
)

// Criterion: a request naming a registered Project, in any case, is recorded
// with the registered spelling, for searches, Misses and lookups alike.
func TestEventStoresTheRegisteredSpellingOfItsProject(t *testing.T) {
	pub := streamHandler(t, platform.Config{ContentDir: spellingContent})
	for _, c := range []struct {
		path   string
		status int
	}{
		{"/api/v1/search?q=kubernetes&project=github.com/acme/shop", http.StatusOK},
		{"/api/v1/search?q=kubernetes&project=github.com/ACME/SHOP", http.StatusOK},
		{"/api/v1/search?q=cobol&project=github.com/aCmE/sHoP", http.StatusOK},
		{"/api/v1/articles/kubernetes?project=github.com/acme/shop", http.StatusOK},
		{"/api/v1/articles/no-such-article?project=github.com/ACME/shop", http.StatusNotFound},
	} {
		if e := lastEvent(t, pub, c.path, c.status, nil); e.Project != registeredSpelling {
			t.Errorf("%s: recorded project %q, want the registered spelling %q", c.path, e.Project, registeredSpelling)
		}
	}
}

// Criterion: an unregistered Project is recorded lower-cased, so the same id
// sent in two cases is one Project, and its Misses are counted together.
func TestEventStoresAnUnregisteredProjectLowerCased(t *testing.T) {
	pub := streamHandler(t, platform.Config{ContentDir: spellingContent})
	for _, path := range []string{
		"/api/v1/search?q=cobol&project=github.com/ACME/None",
		"/api/v1/search?q=cobol&project=github.com/acme/none",
		"/api/v1/articles/kubernetes?project=github.com/Acme/NONE",
	} {
		if e := lastEvent(t, pub, path, http.StatusOK, nil); e.Project != "github.com/acme/none" {
			t.Errorf("%s: recorded project %q, want the unregistered id lower-cased, github.com/acme/none", path, e.Project)
		}
	}
	if e := lastEvent(t, pub, "/api/v1/search?q=cobol", http.StatusOK, nil); e.Project != "" {
		t.Errorf("a search without a project recorded project %q, want \"\"", e.Project)
	}
}

// Criterion: the stream shows the stored spelling, as text.
func TestActivityStreamShowsTheRegisteredSpelling(t *testing.T) {
	pub := streamHandler(t, platform.Config{ContentDir: spellingContent})
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	if rec := serveAs(t, pub, "/api/v1/search?q=kubernetes&project=github.com/ACME/shop", func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: status %d", rec.Code)
	}
	resp, s, body := openStream(t, t.Context(), http.DefaultClient, srv.URL+activityPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	if e := decodeEvent(t, nextEvent(t, s, 5*time.Second)); e.Project != registeredSpelling {
		t.Errorf("streamed project %q, want %q", e.Project, registeredSpelling)
	}
	if rec := serveAs(t, pub, "/api/v1/search?q=cobol&project=github.com/ACME/None", func(*http.Request) {}); rec.Code != http.StatusOK {
		t.Fatalf("fixture: status %d", rec.Code)
	}
	if e := decodeEvent(t, nextEvent(t, s, 5*time.Second)); e.Project != "github.com/acme/none" {
		t.Errorf("streamed project %q, want github.com/acme/none", e.Project)
	}
}
