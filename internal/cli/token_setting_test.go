package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #533: goldie version and the status auth line name a
// broken token setting (both GOLDIE_TOKEN and GOLDIE_TOKEN_FILE set, or a
// token file that is unreadable, empty or whitespace only) instead of "no
// token given", which tells the user to set a variable they already set. A
// missing and a rejected token read as they do today. The cut is cli.Run
// against a stub server that wants a token.

const (
	brokenTokenPath  = "/run/secrets/leak-path-marker/token"
	brokenTokenValue = "leak-token-value-marker"
)

// tokenWallServer answers every request 401: the server wants a token and
// accepts none the test sends.
func tokenWallServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"unauthorized","details":{"hint":"send a token"}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runWithEnv runs args with the variables vars and a file reader readFile (nil
// keeps the default), in an empty working directory, and returns what the
// command printed.
func runWithEnv(t *testing.T, vars map[string]string, readFile func(string) ([]byte, error), args ...string) (string, error) {
	t.Helper()
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	env := localEnv(vars, &out)
	if readFile != nil {
		env.ReadFile = readFile
	}
	err := cli.Run(context.Background(), args, env)
	return out.String(), err
}

// brokenTokenSettings are the settings the criteria call broken, as variables
// and the file reader that serves them.
var brokenTokenSettings = map[string]struct {
	vars     map[string]string
	readFile func(string) ([]byte, error)
	conflict bool // both variables set
}{
	"both token and token file": {
		vars:     map[string]string{platform.EnvToken: brokenTokenValue, platform.EnvToken + "_FILE": brokenTokenPath},
		readFile: func(string) ([]byte, error) { return []byte(brokenTokenValue), nil },
		conflict: true,
	},
	"unreadable token file": {
		vars:     map[string]string{platform.EnvToken + "_FILE": brokenTokenPath},
		readFile: func(string) ([]byte, error) { return nil, errors.New("open " + brokenTokenPath + ": permission denied") },
	},
	"empty token file": {
		vars:     map[string]string{platform.EnvToken + "_FILE": brokenTokenPath},
		readFile: func(string) ([]byte, error) { return nil, nil },
	},
	"newline only token file": {
		vars:     map[string]string{platform.EnvToken + "_FILE": brokenTokenPath},
		readFile: func(string) ([]byte, error) { return []byte("\r\n\n"), nil },
	},
	"whitespace only token file": {
		vars:     map[string]string{platform.EnvToken + "_FILE": brokenTokenPath},
		readFile: func(string) ([]byte, error) { return []byte("  \t \n"), nil },
	},
}

// requireBrokenWords checks out names the broken setting for the case: the
// conflict names both variables and says to set only one, a bad file says the
// token file can't be used. It must not read as a missing token and must not
// leak the path or any content.
func requireBrokenWords(t *testing.T, what, out string, conflict bool) {
	t.Helper()
	low := strings.ToLower(out)
	if strings.Contains(low, "no token given") {
		t.Errorf("%s = %q, must not say the token is missing: the user set it", what, out)
	}
	if conflict {
		if !strings.Contains(out, "GOLDIE_TOKEN_FILE") || !strings.Contains(low, "only one") {
			t.Errorf("%s = %q, want the conflict named: GOLDIE_TOKEN and GOLDIE_TOKEN_FILE, set only one", what, out)
		}
	} else if !strings.Contains(low, "token file") || (!strings.Contains(low, "can't be used") && !strings.Contains(low, "cannot be used")) {
		t.Errorf("%s = %q, want it to say the token file can't be used", what, out)
	}
	for _, leak := range []string{brokenTokenPath, "leak-path-marker", brokenTokenValue, "permission denied"} {
		if strings.Contains(out, leak) {
			t.Errorf("%s leaks %q: %q", what, leak, out)
		}
	}
}

func TestCLIVersionNamesABrokenTokenSetting(t *testing.T) {
	srv := tokenWallServer(t)
	for name, c := range brokenTokenSettings {
		t.Run(name, func(t *testing.T) {
			out, err := runWithEnv(t, c.vars, c.readFile, "version", "--addr", srv.URL)
			if err != nil {
				t.Fatalf("version must exit 0: %v", err)
			}
			requireBrokenWords(t, "version", out, c.conflict)
			if !strings.Contains(out, "server API: unknown (") {
				t.Errorf("version = %q, want the unknown server API line kept", out)
			}
		})
	}
}

func TestCLIVersionJSONCarriesABrokenTokenSetting(t *testing.T) {
	srv := tokenWallServer(t)
	for name, c := range brokenTokenSettings {
		t.Run(name, func(t *testing.T) {
			out, err := runWithEnv(t, c.vars, c.readFile, "version", "--json", "--addr", srv.URL)
			if err != nil {
				t.Fatalf("version --json must exit 0: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatalf("version --json is not JSON: %v\n%s", err, out)
			}
			if v, present := m["serverApiVersion"]; !present || v != nil {
				t.Errorf("serverApiVersion = %v (present %v), want null", v, present)
			}
			note, _ := m["serverNote"].(string)
			if note == "" {
				t.Errorf("serverNote is empty in %s, want the broken state", out)
			}
			requireBrokenWords(t, "version --json serverNote", note, c.conflict)
			requireBrokenWords(t, "version --json", out, c.conflict)
		})
	}
}

func TestCLIStatusShowsABrokenTokenSettingOnItsAuthLine(t *testing.T) {
	srv := tokenWallServer(t)
	for name, c := range brokenTokenSettings {
		t.Run(name, func(t *testing.T) {
			out, err := runWithEnv(t, c.vars, c.readFile, "status", "--addr", srv.URL)
			if err != nil {
				t.Fatalf("status must report the state, not fail: %v", err)
			}
			var auth string
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, "Auth: ") {
					auth = line
				}
			}
			if auth == "" {
				t.Fatalf("status = %q, want an Auth: line", out)
			}
			requireBrokenWords(t, "status auth line", auth, c.conflict)
			requireBrokenWords(t, "status", out, c.conflict)
		})
	}
}

// A missing token and a rejected token read as they do today, in version and
// in status.
func TestCLIMissingAndRejectedTokenStillReadAsToday(t *testing.T) {
	srv := tokenWallServer(t)
	cases := map[string]struct {
		vars       map[string]string
		versionNot string // version's note
		statusAuth string
	}{
		"missing": {nil, "server API: unknown (no token given)", "Auth: no token given"},
		"rejected": {map[string]string{platform.EnvToken: "rejected-token-value"},
			"server API: unknown (the token was rejected)", "Auth: token rejected"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := runWithEnv(t, c.vars, nil, "version", "--addr", srv.URL)
			if err != nil || !strings.Contains(out, c.versionNot) {
				t.Errorf("version = %q, %v, want %q", out, err, c.versionNot)
			}
			out, err = runWithEnv(t, c.vars, nil, "status", "--addr", srv.URL)
			if err != nil || !strings.Contains(out, c.statusAuth) {
				t.Errorf("status = %q, %v, want %q", out, err, c.statusAuth)
			}
		})
	}
}
