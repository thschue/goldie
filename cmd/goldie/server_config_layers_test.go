package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/pflag"

	"github.com/thschue/goldie/internal/platform"
)

// layersFrom builds the layered Lookup of a server run: args are flags, env the
// environment, file the config file's text.
func layersFrom(t *testing.T, args []string, env map[string]string, file string) (*platform.Layers, error) {
	t.Helper()
	fs := pflag.NewFlagSet("server", pflag.ContinueOnError)
	platform.AddConfigFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	if file != "" {
		env["GOLDIE_CONFIG"] = configFile(t, file)
	}
	return platform.NewLayers(fs, func(k string) (string, bool) { v, ok := env[k]; return v, ok }, os.ReadFile)
}

// Telemetry reads the layered lookup: the client-info switch set in the config
// file or by flag takes effect on the exported spans (OTEL_* stay environment).
func TestServerTelemetryReadsFlagsAndTheConfigFile(t *testing.T) {
	var mu sync.Mutex
	bodies := map[string][]byte{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies[r.URL.Path] = append(bodies[r.URL.Path], b...)
		mu.Unlock()
	}))
	defer collector.Close()
	file := baseFile(t) + "telemetry_client_info: true\n"
	layers, err := layersFrom(t, nil, map[string]string{"OTEL_SERVICE_NAME": "layered-svc", "OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL, "OTEL_LOGS_EXPORTER": "none", "OTEL_METRICS_EXPORTER": "none"}, file)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, _, tel, err := serverSetup(layers.Lookup(), layers.Annotate, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !tel.Enabled() {
		t.Fatal("telemetry set in the config file is off")
	}
	h := tel.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	req.RemoteAddr = "192.0.2.7:4321"
	req.Header.Set("User-Agent", "layered-agent")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	got := bodies["/v1/traces"]
	for _, want := range []string{"layered-svc", "client.address", "192.0.2.7", "user_agent.original"} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("exported trace lacks %q", want)
		}
	}
}

// A bad telemetry value from a flag stops the server naming the setting and
// the flag, never the value.
func TestServerTelemetryErrorNamesTheFlagNeverTheValue(t *testing.T) {
	layers, err := layersFrom(t, []string{"--telemetry-client-info", "secret-value"}, map[string]string{}, baseFile(t))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = serverSetup(layers.Lookup(), layers.Annotate, io.Discard)
	if err == nil {
		t.Fatal("a bad value did not stop the server")
	}
	msg := err.Error()
	if !strings.Contains(msg, "GOLDIE_TELEMETRY_CLIENT_INFO") || !strings.Contains(msg, "--telemetry-client-info") || strings.Contains(msg, "secret-value") {
		t.Errorf("error = %q", msg)
	}
}

// A *_FILE flag that cannot be read is attributed to that flag, not to the
// secret's own flag the user never passed.
func TestServerFileFlagErrorsNameTheFileFlag(t *testing.T) {
	for _, s := range platform.Settings {
		if !s.Secret {
			continue
		}
		flag := "--" + strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(s.Name, "GOLDIE_")), "_", "-") + "-file"
		t.Run(s.Name, func(t *testing.T) {
			layers, err := layersFrom(t, []string{flag, "/nonexistent/secret"}, map[string]string{"GOLDIE_CONTENT_DIR": contentDir(t)}, "")
			if err != nil {
				t.Fatal(err)
			}
			_, err = platform.LoadConfig(layers.Lookup())
			if err == nil {
				t.Skip("this setting is not read by LoadConfig")
			}
			msg := layers.Annotate(err).Error()
			want := s.Name + "_FILE given as the flag " + flag
			if !strings.Contains(msg, want) {
				t.Errorf("error %q lacks %q", msg, want)
			}
			if strings.Contains(msg, s.Name+" given as the flag") {
				t.Errorf("error %q names a flag the user never passed", msg)
			}
		})
	}
}

// A git URL with credentials is refused when a flag or the config file gives
// it, naming the flag or key and never the URL.
func TestServerRefusesGitURLCredentialsFromAFlagOrTheFile(t *testing.T) {
	const pw = "hunter2"
	userinfo := "https://bob:" + pw + "@example.com/org/repo.git"
	cases := []struct {
		name  string
		args  []string
		file  string
		wants string
	}{
		{"flag git-url", []string{"--git-url", userinfo}, "", "--git-url"},
		{"flag git-url user only", []string{"--git-url", "https://bob@example.com/r.git"}, "", "--git-url"},
		{"flag git-url ssh password", []string{"--git-url", "ssh://bob:" + pw + "@example.com/r.git"}, "", "--git-url"},
		{"file git_url", nil, "git_url: " + userinfo + "\n", "git_url"},
		{"flag sources", []string{"--sources", `[{"name":"a","type":"git","url":"` + userinfo + `"}]`}, "", "--sources[0].url"},
		{"file sources", nil, "sources:\n  - name: a\n    type: dir\n    dir: .\n  - name: b\n    type: git\n    url: " + userinfo + "\n", "sources[1].url"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := layersFrom(t, c.args, map[string]string{}, c.file)
			if err == nil {
				t.Fatal("credentials in a URL were accepted")
			}
			if !strings.Contains(err.Error(), c.wants) || strings.Contains(err.Error(), pw) || strings.Contains(err.Error(), "bob") {
				t.Errorf("error = %q, want one naming %q and no value", err, c.wants)
			}
		})
	}
	for name, args := range map[string][]string{
		"ssh user only": {"--git-url", "ssh://git@example.com/r.git"},
		"scp-like":      {"--git-url", "git@example.com:org/r.git"},
		"plain https":   {"--git-url", "https://example.com/org/r.git"},
	} {
		if _, err := layersFrom(t, args, map[string]string{}, ""); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
}

// The refusal says what to do next on the surface the user used.
func TestServerGitURLCredentialsErrorSaysTheNextStepPerSurface(t *testing.T) {
	url := "https://bob:hunter2@example.com/r.git"
	_, err := layersFrom(t, []string{"--git-url", url}, map[string]string{}, "")
	if err == nil || !strings.Contains(err.Error(), "when set as a flag, which shows in the process list: name the token with --git-token-file") {
		t.Errorf("flag error = %v", err)
	}
	_, err = layersFrom(t, nil, map[string]string{}, "git_url: "+url+"\n")
	if err == nil || !strings.Contains(err.Error(), "in the config file, which is easy to share: use git_token_file") {
		t.Errorf("file error = %v", err)
	}
	_, err = layersFrom(t, []string{"--sources", `[{"name":"a","type":"git","url":"` + url + `"}]`}, map[string]string{}, "")
	if err == nil || !strings.Contains(err.Error(), "name the token with token_env on the Source") {
		t.Errorf("sources flag error = %v", err)
	}
}
