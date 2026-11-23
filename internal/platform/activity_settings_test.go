package platform

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Acceptance tests for #245 (ADR 0022), slice 2 (#398): the federated
// activity settings.
// GOLDIE_ACTIVITY_PEERS is the headless-Service DNS name the server resolves
// to find its peers for GET /api/v1/activity?replicas=all; unset, only the
// local stream is served. GOLDIE_ACTIVITY_REORDER_WINDOW is how long live
// events wait so older ones from other replicas can overtake them, as a Go
// duration (2s by default, at most 1m0s). Neither is a secret; both have the _FILE form
// every variable has (PC-05), fail startup on a bad value naming the variable
// and never echoing the value (PC-04), and are reported as written (PC-06).
// The contract they fix:
//
//	const EnvActivityPeers          = "GOLDIE_ACTIVITY_PEERS"
//	const EnvActivityReorderWindow  = "GOLDIE_ACTIVITY_REORDER_WINDOW"
//	type Config struct {
//		...
//		ActivityPeers         string        // the DNS name as written; "" for no peers
//		ActivityReorderWindow time.Duration // parsed, 2s when unset
//	}
const (
	envActivityPeers         = "GOLDIE_ACTIVITY_PEERS"
	envActivityReorderWindow = "GOLDIE_ACTIVITY_REORDER_WINDOW"
)

// Both are server settings in the registry, neither a secret. The peers
// setting is unset by default (one replica, the local stream), and its
// description, rendered into the configuration reference, says what it is,
// what it turns on, and that forwarding the caller's bearer token between
// replicas needs TLS or a NetworkPolicy (ADR 0022). The window defaults to
// 2s and says what happens to an event later than it.
func TestActivitySettingsAreRegisteredAndDocumented(t *testing.T) {
	peers := settingRow(t, envActivityPeers)
	if peers.Default != "" {
		t.Errorf("%s default = %q, want unset: no peers unless the operator names them", envActivityPeers, peers.Default)
	}
	if peers.Secret {
		t.Errorf("%s is marked Secret; it is a DNS name, not a secret", envActivityPeers)
	}
	for _, want := range []string{"headless", "replicas=all", "TLS", "NetworkPolicy", "token"} {
		if !strings.Contains(peers.Meaning, want) {
			t.Errorf("%s description %q does not state %q", envActivityPeers, peers.Meaning, want)
		}
	}

	window := settingRow(t, envActivityReorderWindow)
	if window.Default != "2s" {
		t.Errorf("%s default = %q, want 2s (ADR 0022)", envActivityReorderWindow, window.Default)
	}
	if window.Secret {
		t.Errorf("%s is marked Secret; it is a duration", envActivityReorderWindow)
	}
	if !strings.Contains(window.Meaning, "late") {
		t.Errorf("%s description %q does not say that later events are marked late", envActivityReorderWindow, window.Meaning)
	}
	for _, s := range ClientSettings {
		if s.Name == envActivityPeers || s.Name == envActivityReorderWindow {
			t.Errorf("%s is in ClientSettings; it configures the server", s.Name)
		}
	}
	ref := committedReference(t)
	for _, name := range []string{envActivityPeers, envActivityReorderWindow} {
		if !strings.Contains(ref, "\n| `"+name+"` | ") {
			t.Errorf("committed docs/reference/configuration.md has no row for %s (run: ./run regen)", name)
		}
	}
}

// Unset, there are no peers and the window is 2s.
func TestActivitySettingsUnsetMeanNoPeersAndATwoSecondWindow(t *testing.T) {
	for _, env := range []map[string]string{{}, {envActivityPeers: "", envActivityReorderWindow: ""}} {
		cfg, err := loadEnv(env)
		if err != nil {
			t.Fatalf("%v: %v, want the defaults", env, err)
		}
		if cfg.ActivityPeers != "" {
			t.Errorf("%v: ActivityPeers = %q, want none", env, cfg.ActivityPeers)
		}
		if cfg.ActivityReorderWindow != 2*time.Second {
			t.Errorf("%v: ActivityReorderWindow = %v, want 2s", env, cfg.ActivityReorderWindow)
		}
		if got := reported(t, cfg, envActivityReorderWindow); got != "2s" {
			t.Errorf("%v: the window is reported as %q, want 2s", env, got)
		}
	}
}

// A DNS name is accepted as written, from the variable or its _FILE form, and
// reported as written.
func TestActivityPeersAcceptsADNSName(t *testing.T) {
	for _, v := range []string{
		"goldie",
		"goldie-headless",
		"goldie-headless.goldie.svc",
		"goldie-headless.goldie.svc.cluster.local",
		"a1-b2.c3",
		strings.Repeat("a", 63) + ".svc",
	} {
		cfg, err := loadEnv(map[string]string{envActivityPeers: v})
		if err != nil {
			t.Errorf("%s=%q: %v, want it accepted", envActivityPeers, v, err)
			continue
		}
		if cfg.ActivityPeers != v {
			t.Errorf("%s=%q: ActivityPeers = %q", envActivityPeers, v, cfg.ActivityPeers)
		}
		if got := reported(t, cfg, envActivityPeers); got != v {
			t.Errorf("%s=%q: reported %q", envActivityPeers, v, got)
		}
	}
	path := t.TempDir() + "/peers"
	if err := os.WriteFile(path, []byte("goldie-headless.goldie.svc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadEnv(map[string]string{envActivityPeers + "_FILE": path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActivityPeers != "goldie-headless.goldie.svc" {
		t.Errorf("_FILE form: ActivityPeers = %q", cfg.ActivityPeers)
	}
}

// Anything that is not a DNS name fails startup naming the variable, never
// echoing the value: a URL, a path, a port, userinfo, a query, whitespace,
// control bytes, empty or overlong labels, labels that start or end with a
// hyphen, underscores, wildcards, IPv6 literals, non-ASCII and hidden runes.
// The credential goes to what this resolves to, so nothing but a name is
// taken.
func TestActivityPeersRejectsAnythingButADNSName(t *testing.T) {
	for _, v := range []string{
		"marker://goldie",
		"http://marker.goldie.svc",
		"https://marker.goldie.svc",
		"marker.goldie.svc/path",
		"marker.goldie.svc:8080",
		"user@marker.goldie.svc",
		"marker.goldie.svc?x=1",
		"marker.goldie.svc#x",
		"marker goldie",
		" marker",
		"marker ",
		"marker\n",
		"marker\t.svc",
		"marker\x00.svc",
		"-marker.svc",
		"marker-.svc",
		"marker..svc",
		".marker.svc",
		"marker.svc..",
		"mark_er.svc",
		"*.marker.svc",
		"marker," + "other.svc",
		"[::1]marker",
		"marker" + strings.Repeat("a", 58) + ".svc", // a 64-byte label
		strings.Repeat("marker.", 37) + "svc",       // 262 bytes, over 253
		"m\u00e4rker.svc",
		"marker\u200b.svc",
		"marker\u202e.svc",
		"\uff4darker.svc", // fullwidth m
	} {
		_, err := loadEnv(map[string]string{envActivityPeers: v})
		if err == nil || !strings.Contains(err.Error(), envActivityPeers) {
			t.Errorf("%s=%q: err = %v, want a failure naming %s", envActivityPeers, v, err, envActivityPeers)
			continue
		}
		if strings.Contains(err.Error(), "marker") || strings.Contains(err.Error(), "arker") {
			t.Errorf("%s=%q: error echoes the value: %v", envActivityPeers, v, err)
		}
	}
}

// A positive Go duration up to one minute, inclusive, is accepted, from the variable or its _FILE form,
// and reported as written.
func TestActivityReorderWindowAcceptsAPositiveDuration(t *testing.T) {
	for v, want := range map[string]time.Duration{
		"2s":    2 * time.Second,
		"500ms": 500 * time.Millisecond,
		"1.5s":  1500 * time.Millisecond,
		"250ms": 250 * time.Millisecond,
		"1m0s":  time.Minute,
		"60s":   time.Minute,
		"59.9s": 59900 * time.Millisecond,
	} {
		cfg, err := loadEnv(map[string]string{envActivityReorderWindow: v})
		if err != nil {
			t.Errorf("%s=%q: %v, want it accepted", envActivityReorderWindow, v, err)
			continue
		}
		if cfg.ActivityReorderWindow != want {
			t.Errorf("%s=%q: ActivityReorderWindow = %v, want %v", envActivityReorderWindow, v, cfg.ActivityReorderWindow, want)
		}
		if got := reported(t, cfg, envActivityReorderWindow); got != v {
			t.Errorf("%s=%q: reported %q", envActivityReorderWindow, v, got)
		}
	}
	path := t.TempDir() + "/window"
	if err := os.WriteFile(path, []byte("750ms\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadEnv(map[string]string{envActivityReorderWindow + "_FILE": path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActivityReorderWindow != 750*time.Millisecond {
		t.Errorf("_FILE form: ActivityReorderWindow = %v, want 750ms", cfg.ActivityReorderWindow)
	}
}

// Zero, negative, over one minute, unitless, malformed or overflowing
// durations fail startup
// naming the variable, never echoing the value (time.ParseDuration's own
// error quotes it).
func TestActivityReorderWindowRejectsABadDuration(t *testing.T) {
	for _, v := range []string{
		"0",
		"0s",
		"-1s",
		"-2marker",
		"2",
		"marker",
		"2marker",
		"2 s",
		" 2s",
		"2s ",
		"2s\n",
		"2s\x00",
		"2S",
		"1e3s",
		"two seconds",
		"\u0662s", // Arabic-Indic digit two
		"\uff12s", // fullwidth digit two
		"9999999999999999h",
		"1m1s",
		"60.001s",
		"61s",
		"2m",
		"1h",
	} {
		_, err := loadEnv(map[string]string{envActivityReorderWindow: v})
		if err == nil || !strings.Contains(err.Error(), envActivityReorderWindow) {
			t.Errorf("%s=%q: err = %v, want a failure naming %s", envActivityReorderWindow, v, err, envActivityReorderWindow)
			continue
		}
		if strings.Contains(err.Error(), "marker") || strings.Contains(err.Error(), `"`+v+`"`) {
			t.Errorf("%s=%q: error echoes the value: %v", envActivityReorderWindow, v, err)
		}
	}
}

// The _FILE form follows PC-05 for both: setting a variable and its _FILE
// form together, or naming a file that cannot be read, fails startup naming
// the variable, never the value or the file's path; a bad value read from the
// file is rejected like the variable's own, without echoing it.
func TestActivitySettingsFileFormFailsWithoutEchoing(t *testing.T) {
	dir := t.TempDir()
	missing := dir + "/marker-missing"
	bad := dir + "/marker-bad"
	if err := os.WriteFile(bad, []byte("http://marker.goldie.svc:8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	badWindow := dir + "/marker-window"
	if err := os.WriteFile(badWindow, []byte("2marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		env  map[string]string
	}{
		{envActivityPeers, map[string]string{envActivityPeers: "marker.svc", envActivityPeers + "_FILE": bad}},
		{envActivityPeers, map[string]string{envActivityPeers + "_FILE": missing}},
		{envActivityPeers, map[string]string{envActivityPeers + "_FILE": bad}},
		{envActivityReorderWindow, map[string]string{envActivityReorderWindow: "2s", envActivityReorderWindow + "_FILE": badWindow}},
		{envActivityReorderWindow, map[string]string{envActivityReorderWindow + "_FILE": missing}},
		{envActivityReorderWindow, map[string]string{envActivityReorderWindow + "_FILE": badWindow}},
	} {
		_, err := loadEnv(c.env)
		if err == nil || !strings.Contains(err.Error(), c.name) {
			t.Errorf("%v: err = %v, want a failure naming %s", c.env, err, c.name)
			continue
		}
		if strings.Contains(err.Error(), "marker") || strings.Contains(err.Error(), dir) {
			t.Errorf("%v: error echoes a value or a path: %v", c.env, err)
		}
	}
}
