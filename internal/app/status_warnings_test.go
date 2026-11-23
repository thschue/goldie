package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// #274 criterion 4: the load's warnings are exposed on GET /api/v1/sync for
// operator tokens only (ADR 0008), because they show what a Source wrote.

const statusPath = "/api/v1/sync"

func warningServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	h, err := PublicHandler(platform.Config{
		ContentDir: content,
		APITokens:  namedTokenLines(),
		RateLimit:  platform.DefaultRateLimit(),
	}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestStatusGivesTheWarningsToAnOperator(t *testing.T) {
	srv := warningServer(t, suppressContent)
	resp, body := do(t, http.MethodGet, srv.URL+statusPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", resp.StatusCode, body)
	}
	var got struct {
		Warnings []struct {
			File    string `json:"file"`
			Message string `json:"message"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].File != shopYAML || !strings.Contains(got.Warnings[0].Message, "article:team/gone") {
		t.Fatalf("warnings = %+v, want one for article:team/gone in %s", got.Warnings, shopYAML)
	}
	for _, w := range []string{got.Warnings[0].File, got.Warnings[0].Message} {
		for _, b := range []byte(w) {
			if b < 0x20 || b > 0x7e {
				t.Errorf("warning %q holds the byte %#x", w, b)
			}
		}
	}
}

func TestStatusWithoutWarningsIsAnEmptyList(t *testing.T) {
	srv := warningServer(t, testContent)
	resp, body := do(t, http.MethodGet, srv.URL+statusPath, bearer(operatorToken))
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"warnings":[]}` {
		t.Errorf("status %d body %s, want 200 {\"warnings\":[]}", resp.StatusCode, body)
	}
}

func TestStatusRefusesAReaderAndAnonymous(t *testing.T) {
	srv := warningServer(t, suppressContent)
	for name, c := range map[string]struct {
		auth   func(*http.Request)
		status int
		hint   string
	}{
		"reader":    {bearer(readerToken), http.StatusForbidden, "Use a token with the operator role."},
		"hashed":    {bearer(hashedSecret), http.StatusForbidden, "Use a token with the operator role."},
		"no token":  {nil, http.StatusUnauthorized, ""},
		"bad token": {bearer("wrong-token-presented-here"), http.StatusUnauthorized, ""},
	} {
		resp, body := do(t, http.MethodGet, srv.URL+statusPath, c.auth)
		if resp.StatusCode != c.status {
			t.Errorf("%s: status %d, want %d: %s", name, resp.StatusCode, c.status, body)
		}
		if c.hint != "" {
			if _, hint := errorHint(t, body); hint != c.hint {
				t.Errorf("%s: hint %q, want %q", name, hint, c.hint)
			}
		}
		if strings.Contains(string(body), "team/gone") || strings.Contains(string(body), "project.yaml") {
			t.Errorf("%s: the refusal shows a warning: %s", name, body)
		}
	}
}
