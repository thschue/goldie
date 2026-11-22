package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Acceptance tests for #148: goldie lookup refuses an empty or dot-only id
// with a hint to goldie search, and sends no HTTP request for it.

// countingServer answers every request with a 404 envelope and counts them.
func countingServer(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"not found","details":{}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n
}

func TestCLILookupRejectsEmptyAndDotOnlyIDsWithAHint(t *testing.T) {
	addr, requests := countingServer(t)
	for _, id := range []string{"", ".", "..", "..."} {
		for _, asJSON := range []bool{false, true} {
			name := "id=" + id
			if asJSON {
				name += " --json"
			}
			t.Run(name, func(t *testing.T) {
				before := requests.Load()
				args := []string{"lookup", id, "--addr", addr, "--token", cliToken}
				if asJSON {
					args = append(args, "--json")
				}
				out, err := runCLI(t, args...)
				if err == nil {
					t.Fatalf("lookup %q succeeded, want an error; output %q", id, out)
				}
				if got := requests.Load() - before; got != 0 {
					t.Errorf("lookup %q made %d HTTP request(s), want none", id, got)
				}
				for _, want := range []string{"Hint:", "goldie search"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q lacks %q", err, want)
					}
				}
				if strings.Contains(err.Error(), "404") {
					t.Errorf("error %q is the bare server 404, want a client-side rejection", err)
				}
				if strings.Contains(err.Error(), cliToken) || strings.Contains(err.Error(), "127.0.0.1") {
					t.Errorf("error leaks a token or address: %q", err)
				}
				if out != "" {
					t.Errorf("lookup %q wrote output %q, want none", id, out)
				}
			})
		}
	}
}

// The check runs before any network use: it rejects the id even when the
// server is unreachable, so a typo never costs a round trip.
func TestCLILookupRejectsDotOnlyIDBeforeNeedingAServer(t *testing.T) {
	_, err := runCLI(t, "lookup", "..", "--addr", "http://127.0.0.1:1", "--token", cliToken)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "cannot reach") || !strings.Contains(err.Error(), "goldie search") {
		t.Errorf("error = %q, want the id rejection with the search hint, not a transport error", err)
	}
}

// Ids that merely contain dots are real ids and still reach the server.
func TestCLILookupStillSendsIDsThatContainDots(t *testing.T) {
	for _, id := range []string{"a.b", "v1.2", "platform/deploy.md", ".hidden", "a..b"} {
		t.Run(id, func(t *testing.T) {
			addr, requests := countingServer(t)
			_, err := runCLI(t, "lookup", id, "--addr", addr, "--token", cliToken)
			if err == nil || !strings.Contains(err.Error(), "404") {
				t.Fatalf("lookup %q: err = %v, want the server's 404", id, err)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("lookup %q made %d request(s), want 1", id, got)
			}
		})
	}
}
