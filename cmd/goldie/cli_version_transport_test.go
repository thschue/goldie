package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/contract"
)

// Acceptance tests for #221: goldie version words a redirect and an oversized
// answer as precisely as transportError does, instead of with the
// unreachable-server note and hint. An unreachable server keeps its wording
// (pinned by TestCLIVersionWithAnUnreachableServerHintsToCheckServerAndAddress
// and the version_unreachable goldens). No existing golden or test pinned the
// old wording for a redirect or an oversized answer, so none changes.

const unreachableHint = "check that the server is running and the address is right"

// redirectingDocServer answers every request, the API document included, with
// a redirect whose Location and body carry markers that must never be echoed.
func redirectingDocServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://leak-host.invalid:1/leak-path?token=leak-location")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"error":{"code":"moved","message":"leak-body"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// oversizedDocServer answers the API document with a JSON body over the
// client's response cap.
func oversizedDocServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"info":{"title":"Stub","version":"1"},"pad":"`))
		chunk := []byte(strings.Repeat("a", 1<<20))
		for sent := 0; sent <= contract.MaxResponseBytes; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCLIVersionAgainstARedirectSaysSoAndHintsToCheckTheAddress(t *testing.T) {
	for _, code := range redirectCodes {
		srv := redirectingDocServer(t, code)
		host := mustHost(t, srv.URL)
		for _, args := range [][]string{{"version"}, {"version", "--json"}} {
			t.Run(http.StatusText(code)+"/"+strings.Join(args, " "), func(t *testing.T) {
				out, err := runCLI(t, append(args, "--addr", srv.URL, "--token", versionToken)...)
				if err != nil {
					t.Fatalf("version must exit 0: %v", err)
				}
				low := strings.ToLower(out)
				if !strings.Contains(low, "redirect") {
					t.Errorf("%v = %q, want it to say the server answered with a redirect", args, out)
				}
				if !strings.Contains(low, "address") {
					t.Errorf("%v = %q, want a hint to check the address", args, out)
				}
				if strings.Contains(out, unreachableHint) || strings.Contains(low, "cannot be reached") || strings.Contains(low, "is running") {
					t.Errorf("%v = %q, must not use the unreachable-server wording", args, out)
				}
				for _, leak := range []string{host, versionToken, "leak-host", "leak-path", "leak-location", "leak-body"} {
					if strings.Contains(out, leak) {
						t.Errorf("%v leaks %q: %q", args, leak, out)
					}
				}
			})
		}
		t.Run(http.StatusText(code)+"/json fields", func(t *testing.T) {
			out, err := runCLI(t, "version", "--json", "--addr", srv.URL, "--token", versionToken)
			if err != nil {
				t.Fatal(err)
			}
			m := versionJSON(t, out)
			requireNullServerVersion(t, m)
			if note, _ := m["serverNote"].(string); !strings.Contains(strings.ToLower(note), "redirect") {
				t.Errorf("serverNote = %v, want it to mention the redirect", m["serverNote"])
			}
			hint, _ := m["hint"].(string)
			if !strings.Contains(strings.ToLower(hint), "address") || hint == unreachableHint {
				t.Errorf("hint = %q, want a redirect-specific hint to check the address", hint)
			}
		})
	}
}

func TestCLIVersionAgainstAnOversizedAnswerSaysItWasTooLarge(t *testing.T) {
	srv := oversizedDocServer(t)
	host := mustHost(t, srv.URL)
	for _, args := range [][]string{{"version"}, {"version", "--json"}} {
		out, err := runCLI(t, append(args, "--addr", srv.URL, "--token", versionToken)...)
		if err != nil {
			t.Fatalf("%v must exit 0: %v", args, err)
		}
		low := strings.ToLower(out)
		if !strings.Contains(low, "too large") {
			t.Errorf("%v = %q, want it to say the answer was too large", args, out)
		}
		if strings.Contains(out, unreachableHint) || strings.Contains(low, "cannot be reached") || strings.Contains(low, "redirect") {
			t.Errorf("%v = %q, must not use the unreachable or redirect wording", args, out)
		}
		if strings.Contains(out, host) || strings.Contains(out, versionToken) {
			t.Errorf("%v leaks the address or token: %q", args, out)
		}
	}
	out, err := runCLI(t, "version", "--json", "--addr", srv.URL, "--token", versionToken)
	if err != nil {
		t.Fatal(err)
	}
	m := versionJSON(t, out)
	requireNullServerVersion(t, m)
	if note, _ := m["serverNote"].(string); !strings.Contains(strings.ToLower(note), "too large") {
		t.Errorf("serverNote = %v, want it to say too large", m["serverNote"])
	}
	if hint, _ := m["hint"].(string); hint == unreachableHint {
		t.Errorf("hint = %q, must not be the unreachable-server hint", hint)
	}
}
