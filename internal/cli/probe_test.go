package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// TestZeroValueServerProbeIsNotAnswered pins that a probe nobody ran never
// reads as an answer from the server: `version` declares one before the
// client exists, and a new caller must not mistake it for a reply.
func TestZeroValueServerProbeIsNotAnswered(t *testing.T) {
	var p serverProbe
	if p.answered() {
		t.Fatal("a zero-value serverProbe reads as answered")
	}
	if (serverProbe{}).answered() {
		t.Fatal("a serverProbe{} literal reads as answered")
	}
}

// TestServerProbeStatesOtherThanAnswerAreNotAnswered keeps the negative
// states apart from the answered one.
func TestServerProbeStatesOtherThanAnswerAreNotAnswered(t *testing.T) {
	for name, p := range map[string]serverProbe{
		"missing token":              {tokenState: tokenMissing},
		"rejected token":             {tokenState: tokenRejected},
		"error":                      {err: errors.New("unreachable")},
		"error with unchecked token": {tokenState: tokenUnchecked, err: errors.New("x")},
	} {
		if p.answered() {
			t.Errorf("%s: probe reads as answered", name)
		}
	}
}

// TestProbeServerOnlyAnswersFromTheDocument proves the state probeServer
// sets: a served API document answers, a 401 and a failure do not.
func TestProbeServerOnlyAnswersFromTheDocument(t *testing.T) {
	for _, c := range []struct {
		name     string
		status   int
		body     string
		given    bool
		answered bool
	}{
		{"document", http.StatusOK, `{"info":{"title":"Goldie","version":"1.2.3"}}`, false, true},
		{"401 without token", http.StatusUnauthorized, `{}`, false, false},
		{"401 with token", http.StatusUnauthorized, `{}`, true, false},
		{"server error", http.StatusInternalServerError, `{}`, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			cl, err := contract.NewClientWithResponses(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			p := probeServer(context.Background(), cl, c.given)
			if got := p.answered(); got != c.answered {
				t.Fatalf("answered() = %v, want %v", got, c.answered)
			}
		})
	}
}
