package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Client info on server spans (#89). By default a server span carries no
// peer address, port or user agent, and never a client address taken from
// X-Forwarded-For, which any caller can forge. GOLDIE_TELEMETRY_CLIENT_INFO=true
// adds the peer IP (from RemoteAddr) as client.address and user_agent.original.
const envClientInfo = "GOLDIE_TELEMETRY_CLIENT_INFO"

const (
	spoofedIP = "203.0.113.9"
	peerIP    = "198.51.100.7"
	testAgent = "goldie-clientinfo-test/1.0"
)

// clientInfoAttrs are the span attributes that carry the caller's address,
// port or user agent.
var clientInfoAttrs = []string{
	"client.address", "network.peer.address", "client.port", "network.peer.port", "user_agent.original",
}

// startupErr runs the checks that happen at startup, in order: LoadConfig,
// then SetupTelemetry. It returns the first error.
func startupErr(env map[string]string) error {
	if _, err := LoadConfig(mapLookup(env)); err != nil {
		return err
	}
	tel, err := SetupTelemetry(context.Background(), mapLookup(env))
	if err == nil {
		_ = tel.Shutdown(context.Background())
	}
	return err
}

// spoofedRequest is a request from peerIP that claims to come from spoofedIP.
func spoofedRequest() *http.Request {
	req := httptest.NewRequest("GET", "http://goldie.test/api/v1/items", nil)
	req.RemoteAddr = peerIP + ":43210"
	req.Header.Set("X-Forwarded-For", spoofedIP)
	req.Header.Set("User-Agent", testAgent)
	return req
}

// By default (no opt-in) a request with a forged X-Forwarded-For and a user
// agent yields a server span without any client info, and the forged address
// is in no attribute. The span comes from a real request over the in-memory
// exporter, so RemoteAddr is a real peer.
func TestServerSpanHasNoClientInfoByDefault(t *testing.T) {
	spans := newSpanSink()
	tel := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, &logSink{})
	app, _, stop := startTraced(t, tel, &syncBuffer{})

	req, _ := http.NewRequest("GET", "http://"+app+"/api/v1/items", nil)
	req.Header.Set("X-Forwarded-For", spoofedIP)
	req.Header.Set("User-Agent", testAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	stop()

	got := spans.spans()
	if len(got) != 1 {
		t.Fatalf("got %d spans, want 1", len(got))
	}
	if len(got[0].Attributes) == 0 {
		t.Fatal("span has no attributes: the check below would pass vacuously")
	}
	for _, kv := range got[0].Attributes {
		for _, bad := range clientInfoAttrs {
			if string(kv.Key) == bad {
				t.Errorf("span carries %s=%q by default", kv.Key, kv.Value.String())
			}
		}
		for _, bad := range []string{spoofedIP, testAgent} {
			if strings.Contains(kv.Value.String(), bad) {
				t.Errorf("span attribute %s=%q leaks %q", kv.Key, kv.Value.String(), bad)
			}
		}
	}
}

// tracedBody sends req through a WrapHandler built by SetupTelemetry from env
// (plus a collector for traces) and returns the OTLP trace payload the
// collector received, and what the wrapped handler saw.
func tracedBody(t *testing.T, env map[string]string, req *http.Request) (body string, seen http.Header, remote string) {
	t.Helper()
	collector, got := newCollector(t)
	full := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL,
		"OTEL_LOGS_EXPORTER":          "none",
	}
	for k, v := range env {
		full[k] = v
	}
	tel, err := SetupTelemetry(context.Background(), mapLookup(full))
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	h := tel.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, remote = r.Header.Clone(), r.RemoteAddr
		w.WriteHeader(http.StatusNoContent)
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	got.mu.Lock()
	defer got.mu.Unlock()
	body = string(got.bodies["/v1/traces"])
	if !strings.Contains(body, "http.request.method") {
		t.Fatalf("no server span reached the collector (%d bytes)", len(body))
	}
	return body, seen, remote
}

// The same request with the setting false, or unset, exports no client info
// and never the forged address.
func TestExportedSpanHasNoClientInfoWhenSettingIsFalseOrUnset(t *testing.T) {
	for _, env := range []map[string]string{{}, {envClientInfo: "false"}} {
		body, _, _ := tracedBody(t, env, spoofedRequest())
		for _, bad := range append([]string{spoofedIP, peerIP, testAgent}, clientInfoAttrs...) {
			if strings.Contains(body, bad) {
				t.Errorf("%v: exported span contains %q", env, bad)
			}
		}
	}
}

// With GOLDIE_TELEMETRY_CLIENT_INFO=true the span carries the peer IP from
// RemoteAddr as client.address, and the user agent. The forged
// X-Forwarded-For value, alone or in a chain, appears nowhere.
func TestExportedSpanCarriesPeerIPAndUserAgentOnOptIn(t *testing.T) {
	for _, xff := range []string{spoofedIP, "203.0.113.9, 10.0.0.1", "2001:db8::9"} {
		req := spoofedRequest()
		req.Header.Set("X-Forwarded-For", xff)
		req.Header.Add("X-Forwarded-For", "203.0.113.77")
		body, _, _ := tracedBody(t, map[string]string{envClientInfo: "true"}, req)
		for _, want := range []string{"client.address", peerIP, "user_agent.original", testAgent} {
			if !strings.Contains(body, want) {
				t.Errorf("xff=%q: exported span lacks %q", xff, want)
			}
		}
		for _, bad := range []string{"203.0.113.9", "203.0.113.77", "2001:db8::9", "10.0.0.1"} {
			if strings.Contains(body, bad) {
				t.Errorf("xff=%q: exported span contains X-Forwarded-For data %q", xff, bad)
			}
		}
	}
}

// Only the span changes: the handler behind it sees the original headers and
// RemoteAddr, with the setting on and off.
func TestHandlerSeesOriginalRequestWhateverTheSetting(t *testing.T) {
	for _, env := range []map[string]string{{}, {envClientInfo: "true"}} {
		_, seen, remote := tracedBody(t, env, spoofedRequest())
		if got := seen.Get("X-Forwarded-For"); got != spoofedIP {
			t.Errorf("%v: handler saw X-Forwarded-For %q, want %q", env, got, spoofedIP)
		}
		if got := seen.Get("User-Agent"); got != testAgent {
			t.Errorf("%v: handler saw User-Agent %q, want %q", env, got, testAgent)
		}
		if remote != peerIP+":43210" {
			t.Errorf("%v: handler saw RemoteAddr %q", env, remote)
		}
	}
}

// The setting is a boolean in the registry, default false, and says that the
// address never comes from X-Forwarded-For.
func TestClientInfoSettingIsRegisteredWithDefaultFalse(t *testing.T) {
	for _, s := range Settings {
		if s.Name != envClientInfo {
			continue
		}
		if s.Default != "false" {
			t.Errorf("%s default = %q, want false", envClientInfo, s.Default)
		}
		for _, want := range []string{"true or false", "X-Forwarded-For"} {
			if !strings.Contains(s.Meaning, want) {
				t.Errorf("%s description %q does not state %q", envClientInfo, s.Meaning, want)
			}
		}
		return
	}
	t.Fatalf("%s is not in the Settings registry", envClientInfo)
}

// The generated configuration reference lists the setting (regenerated with
// ./run regen, not edited by hand).
func TestConfigurationReferenceListsClientInfoSetting(t *testing.T) {
	committed, err := os.ReadFile("../../docs/reference/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(committed), "`"+envClientInfo+"`") {
		t.Errorf("docs/reference/configuration.md does not list %s (run: ./run regen)", envClientInfo)
	}
}

// true and false start; anything else fails startup naming the variable and
// never repeating the value, in the plain and the _FILE form.
func TestClientInfoSettingIsValidatedAtStartup(t *testing.T) {
	for _, v := range []string{"true", "false", ""} {
		if err := startupErr(map[string]string{envClientInfo: v}); err != nil {
			t.Errorf("%s=%q: %v, want it accepted", envClientInfo, v, err)
		}
	}
	file := t.TempDir() + "/v"
	if err := os.WriteFile(file, []byte("true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := startupErr(map[string]string{envClientInfo + "_FILE": file}); err != nil {
		t.Errorf("_FILE form: %v, want it accepted", err)
	}

	const marker = "s3cret-marker"
	bad := t.TempDir() + "/bad"
	if err := os.WriteFile(bad, []byte(marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{
		{envClientInfo: marker},
		{envClientInfo: "yes"},
		{envClientInfo: "1"},
		{envClientInfo: "tru"},
		{envClientInfo: "true false"},
		{envClientInfo: "true\x00"},
		{envClientInfo + "_FILE": bad},
	} {
		err := startupErr(env)
		if err == nil || !strings.Contains(err.Error(), envClientInfo) {
			t.Errorf("%q: err = %v, want it to name %s", env, err, envClientInfo)
			continue
		}
		if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), "yes") {
			t.Errorf("%q: error echoes the value: %v", env, err)
		}
	}
}
