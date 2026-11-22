package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/platform"
)

// closedServerAddr returns the address of a server that has been shut down.
func closedServerAddr() string {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	return addr
}

// badAddrs are the addresses that leave the server line out today: none, and
// ones that are not http(s) URLs (GOLDIE_ADDR is also a listen address).
var badAddrs = []string{"", ":8080", "ftp://files.example.invalid", "not a url", "localhost:8080"}

func versionArgs(addr string, extra ...string) []string {
	args := append([]string{"version"}, extra...)
	if addr != "" {
		args = append(args, "--addr", addr)
	}
	return args
}

func TestCLIVersionWithoutAnAddressSaysSoAndHintsAtGoldieAddr(t *testing.T) {
	const want = "server API: unknown (no server address set)"
	for _, addr := range badAddrs {
		out, err := runCLI(t, versionArgs(addr)...)
		if err != nil {
			t.Fatalf("addr %q: version must exit 0: %v", addr, err)
		}
		if first := strings.SplitN(out, "\n", 2)[0]; first != platform.Version {
			t.Errorf("addr %q: first line = %q, want the client version", addr, first)
		}
		if line := serverLine(out); line != want {
			t.Errorf("addr %q: server line = %q, want %q\n%s", addr, line, want, out)
		}
		if !strings.Contains(out, "Hint: ") || !strings.Contains(out, "GOLDIE_ADDR") {
			t.Errorf("addr %q: version = %q, want a Hint: naming GOLDIE_ADDR", addr, out)
		}
		if addr != "" && strings.Contains(out, addr) {
			t.Errorf("addr %q: the address is printed: %q", addr, out)
		}
	}
}

func TestCLIVersionJSONWithoutAnAddressCarriesServerNoteAndHint(t *testing.T) {
	for _, addr := range badAddrs {
		out, err := runCLI(t, versionArgs(addr, "--json")...)
		if err != nil {
			t.Fatalf("addr %q: version --json must exit 0: %v", addr, err)
		}
		m := versionJSON(t, out)
		if m["serverNote"] != "no server address set" {
			t.Errorf("addr %q: serverNote = %v, want %q", addr, m["serverNote"], "no server address set")
		}
		if hint, _ := m["hint"].(string); !strings.Contains(hint, "GOLDIE_ADDR") {
			t.Errorf("addr %q: hint = %q, want it to name GOLDIE_ADDR", addr, hint)
		}
		requireNullServerVersion(t, m)
		if addr != "" && strings.Contains(out, addr) {
			t.Errorf("addr %q: the address is printed: %q", addr, out)
		}
	}
}

// A token without an address must not change the reason: the address is what
// is missing.
func TestCLIVersionWithATokenButNoAddressStillSaysNoAddress(t *testing.T) {
	out, err := runCLI(t, "version", "--token", versionToken)
	if err != nil {
		t.Fatal(err)
	}
	if line := serverLine(out); line != "server API: unknown (no server address set)" {
		t.Errorf("server line = %q\n%s", line, out)
	}
	if strings.Contains(out, versionToken) {
		t.Errorf("version leaks the token: %q", out)
	}
}

func TestCLIVersionWithAnUnreachableServerHintsToCheckServerAndAddress(t *testing.T) {
	addr := closedServerAddr()
	host := mustHost(t, addr)
	const hint = "check that the server is running and the address is right"
	out, err := runCLI(t, "version", "--addr", addr, "--token", versionToken)
	if err != nil {
		t.Fatalf("version must exit 0: %v", err)
	}
	if line := serverLine(out); line != "server API: unknown (the server cannot be reached)" {
		t.Errorf("server line = %q\n%s", line, out)
	}
	if !strings.Contains(out, "Hint: "+hint) {
		t.Errorf("version = %q, want %q", out, "Hint: "+hint)
	}
	if strings.Contains(out, host) || strings.Contains(out, versionToken) {
		t.Errorf("version leaks the address or token: %q", out)
	}

	js, err := runCLI(t, "version", "--json", "--addr", addr, "--token", versionToken)
	if err != nil {
		t.Fatal(err)
	}
	m := versionJSON(t, js)
	if m["hint"] != hint {
		t.Errorf("hint = %v, want %q", m["hint"], hint)
	}
	if m["serverNote"] != "the server cannot be reached" {
		t.Errorf("serverNote = %v", m["serverNote"])
	}
	if strings.Contains(js, host) {
		t.Errorf("json leaks the address: %q", js)
	}
	requireNullServerVersion(t, m)
}

// The accepted, rejected and no-token outputs do not gain the new hints.
func TestCLIVersionOtherCasesDoNotGetTheNewHints(t *testing.T) {
	srv := authedDocServer(t)
	for _, args := range [][]string{
		{"version", "--addr", srv.URL, "--token", versionToken},
		{"version", "--addr", srv.URL, "--token", "wrong-token-0123456789abcdef0123456789abc"},
		{"version", "--addr", srv.URL},
	} {
		out, err := runCLI(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "check that the server is running") || strings.Contains(out, "GOLDIE_ADDR") || strings.Contains(out, "no server address") {
			t.Errorf("%v gained a hint it should not have: %q", args, out)
		}
	}
}

// TestCLIVersionAddrGolden pins both cases, text and JSON. The client version
// varies by build, so it is written <version>.
func TestCLIVersionAddrGolden(t *testing.T) {
	unreachable := closedServerAddr()
	cases := []struct {
		name string
		args []string
	}{
		{"version_no_addr", []string{"version"}},
		{"version_no_addr_json", []string{"version", "--json"}},
		{"version_unreachable", []string{"version", "--addr", unreachable, "--token", versionToken}},
		{"version_unreachable_json", []string{"version", "--json", "--addr", unreachable, "--token", versionToken}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runCLI(t, c.args...)
			if err != nil {
				t.Fatalf("run %v: %v", c.args, err)
			}
			golden(t, c.name, strings.ReplaceAll(out, platform.Version, "<version>"), unreachable)
		})
	}
}
