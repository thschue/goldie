package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/app"
)

// The OpenAPI document takes the same credentials as the API (#101), so
// status and version send the configured token when they fetch it.
func TestCLISendsTheTokenToTheAPIDocument(t *testing.T) {
	var docAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/openapi.json" {
			docAuth = append(docAuth, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"info":{"title":"Stub","version":"1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	for _, cmd := range []string{"status", "version"} {
		if _, err := runCLI(t, cmd, "--addr", srv.URL, "--token", cliToken); err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
	}
	if len(docAuth) != 2 {
		t.Fatalf("/openapi.json fetched %d times, want 2", len(docAuth))
	}
	for _, a := range docAuth {
		if a != "Bearer "+cliToken {
			t.Errorf("/openapi.json got Authorization %q, want the bearer token", a)
		}
	}
}

// Against a server whose only token is a reader, status and version still
// read the API document with that token.
func TestCLIStatusAndVersionWithAReaderToken(t *testing.T) {
	const reader = "cli-reader-token-0123456789abcdef0123456789"
	h, err := app.PublicHandler(app.Options{
		ContentDir: "../../internal/app/testdata/content",
		APITokens:  "cli-reader:reader:" + reader,
	})
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	out, err := runCLI(t, "status", "--addr", srv.URL, "--token", reader)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"Server: reachable", "API: Goldie API", "Auth: token accepted"} {
		if !strings.Contains(out, want) {
			t.Errorf("status = %q, want %q", out, want)
		}
	}
	out, err = runCLI(t, "version", "--addr", srv.URL, "--token", reader, "--json")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out, `"serverApiVersion"`) {
		t.Errorf("version --json = %q, want the server API version", out)
	}
	if strings.Contains(out, reader) {
		t.Errorf("version echoes the token: %q", out)
	}
}
