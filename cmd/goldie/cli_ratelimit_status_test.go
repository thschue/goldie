package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/app"
)

// statusLimitedServer serves the real API document, and answers every /api/
// request with the 429 envelope and the given Retry-After value, so the
// status command's token probe is the request over the limit.
func statusLimitedServer(t *testing.T, retryAfter string) string {
	t.Helper()
	h, err := app.PublicHandler(app.Options{ContentDir: "../../internal/app/testdata/content", APIToken: cliToken})
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Retry-After", retryAfter)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"too many requests for this API token",` +
			`"details":{"hint":"Wait the number of seconds in the Retry-After header, then retry."}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The status token probe over the limit is the 429, with the wait when the
// header gives one, and not a failure to reach the server when it does not:
// the generated parser rejects a Retry-After that is not an integer.
func TestCLIStatusProbeOverTheLimitIsThe429(t *testing.T) {
	cases := map[string]string{"7": "7 seconds", "soon-marker": ""}
	// Every server first: runCLI changes the working directory, and the
	// content directory is relative to this one.
	urls := map[string]string{}
	for value := range cases {
		urls[value] = statusLimitedServer(t, value)
	}
	for value, want := range cases {
		_, err := runCLI(t, "status", "--addr", urls[value], "--token", cliToken)
		if err == nil {
			t.Errorf("Retry-After %q: expected an error", value)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "429") || strings.Contains(msg, "cannot reach") {
			t.Errorf("Retry-After %q: error %q is not the 429", value, msg)
		}
		if want != "" && !strings.Contains(msg, want) {
			t.Errorf("Retry-After %q: error %q lacks %q", value, msg, want)
		}
		if strings.Contains(msg, "marker") || strings.Contains(msg, "0 seconds") {
			t.Errorf("Retry-After %q: error %q echoes the value or claims a wait", value, msg)
		}
	}
}
