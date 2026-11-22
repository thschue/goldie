package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

const versionToken = "version-token-0123456789abcdef0123456789"

// authedDocServer serves the API document only to versionToken, as a server
// does once the document takes reader auth (#101), and answers anything else
// with the 401 envelope.
func authedDocServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+versionToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"unauthorized","details":{"hint":"send a token"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"info":{"title":"Stub","version":"7.8.9"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// serverLine returns the line of out that begins with "server" (the server
// API line), or "".
func serverLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.ToLower(l), "server") {
			return l
		}
	}
	return ""
}

func versionJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("version --json is not JSON: %v\n%q", err, out)
	}
	return m
}

// The issue names the key serverVersion; the accepted-token output already
// uses serverApiVersion. Either spelling must carry null, and neither may
// carry a value.
func requireNullServerVersion(t *testing.T, m map[string]any) {
	t.Helper()
	found := false
	for _, k := range []string{"serverVersion", "serverApiVersion"} {
		v, ok := m[k]
		if !ok {
			continue
		}
		found = true
		if v != nil {
			t.Errorf("%s = %v, want null", k, v)
		}
	}
	if !found {
		t.Errorf("version --json = %v, want a null serverVersion", m)
	}
}

func TestCLIVersionWithoutATokenSaysTheServerAPIVersionIsUnknown(t *testing.T) {
	srv := authedDocServer(t)
	out, err := runCLI(t, "version", "--addr", srv.URL)
	if err != nil {
		t.Fatalf("version without a token must exit 0: %v", err)
	}
	if first := strings.SplitN(out, "\n", 2)[0]; first != platform.Version {
		t.Errorf("first line = %q, want the client version %q", first, platform.Version)
	}
	if line := serverLine(out); !strings.Contains(strings.ToLower(line), "unknown") {
		t.Errorf("version = %q, want a server line saying the API version is unknown", out)
	}
	if !strings.Contains(out, "Hint: ") || !strings.Contains(out, "GOLDIE_TOKEN") || !strings.Contains(out, "GOLDIE_TOKEN_FILE") {
		t.Errorf("version = %q, want a Hint: naming GOLDIE_TOKEN and GOLDIE_TOKEN_FILE", out)
	}
	if strings.Contains(strings.ToLower(out), "rejected") || strings.Contains(out, "7.8.9") {
		t.Errorf("version = %q, must not claim a rejection or show a version", out)
	}
	if strings.Contains(out, "127.0.0.1") {
		t.Errorf("version prints the address: %q", out)
	}
}

func TestCLIVersionJSONWithoutATokenCarriesNullAndTheHint(t *testing.T) {
	srv := authedDocServer(t)
	out, err := runCLI(t, "version", "--addr", srv.URL, "--json")
	if err != nil {
		t.Fatalf("version --json without a token must exit 0: %v", err)
	}
	m := versionJSON(t, out)
	if v, _ := m["version"].(string); v != platform.Version {
		t.Errorf("version = %v, want the client version %q", m["version"], platform.Version)
	}
	requireNullServerVersion(t, m)
	hint, _ := m["hint"].(string)
	if !strings.Contains(hint, "GOLDIE_TOKEN") || !strings.Contains(hint, "GOLDIE_TOKEN_FILE") {
		t.Errorf("hint = %q, want it to name GOLDIE_TOKEN and GOLDIE_TOKEN_FILE", hint)
	}
}

func TestCLIVersionWithARejectedTokenSaysSo(t *testing.T) {
	srv := authedDocServer(t)
	const bad = "wrong-token-0123456789abcdef0123456789abc"
	for _, args := range [][]string{{"version"}, {"version", "--json"}} {
		out, err := runCLI(t, append(args, "--addr", srv.URL, "--token", bad)...)
		if err != nil {
			t.Fatalf("%v must exit 0: %v", args, err)
		}
		if !strings.Contains(strings.ToLower(out), "rejected") {
			t.Errorf("%v = %q, want it to say the token was rejected", args, out)
		}
		if strings.Contains(out, bad) || strings.Contains(out, "127.0.0.1") {
			t.Errorf("%v leaks the token or address: %q", args, out)
		}
		if strings.Contains(out, "7.8.9") || strings.Contains(strings.ToLower(out), "unreachable") {
			t.Errorf("%v = %q, must not show a version or call the server unreachable", args, out)
		}
	}
	out, err := runCLI(t, "version", "--json", "--addr", srv.URL, "--token", bad)
	if err != nil {
		t.Fatal(err)
	}
	requireNullServerVersion(t, versionJSON(t, out))
}

func TestCLIVersionWithAnUnreachableServerSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	host := mustHost(t, addr)
	srv.Close()
	for _, args := range [][]string{{"version"}, {"version", "--json"}} {
		out, err := runCLI(t, append(args, "--addr", addr, "--token", versionToken)...)
		if err != nil {
			t.Fatalf("%v must exit 0: %v", args, err)
		}
		low := strings.ToLower(out)
		if !strings.Contains(low, "reach") {
			t.Errorf("%v = %q, want it to say the server cannot be reached", args, out)
		}
		if strings.Contains(out, host) || strings.Contains(out, versionToken) {
			t.Errorf("%v leaks the address or token: %q", args, out)
		}
		if strings.Contains(low, "rejected") {
			t.Errorf("%v = %q, must not claim a rejection", args, out)
		}
	}
	out, err := runCLI(t, "version", "--json", "--addr", addr, "--token", versionToken)
	if err != nil {
		t.Fatal(err)
	}
	requireNullServerVersion(t, versionJSON(t, out))
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// With an accepted token the output is unchanged: the version, no hint.
func TestCLIVersionWithAnAcceptedTokenHasNoHint(t *testing.T) {
	srv := authedDocServer(t)
	out, err := runCLI(t, "version", "--addr", srv.URL, "--token", versionToken)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "server API: 7.8.9\n") || strings.Contains(out, "Hint:") {
		t.Errorf("version = %q, want the server API version and no hint", out)
	}
}
