package platform

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// layersFor builds Layers over flag arguments, an environment map and a config
// file's text ("" for no file).
func layersFor(t *testing.T, args []string, env map[string]string, file string) (*Layers, error) {
	t.Helper()
	fs := pflag.NewFlagSet("server", pflag.ContinueOnError)
	AddConfigFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	e := map[string]string{}
	for k, v := range env {
		e[k] = v
	}
	if file != "" {
		e[EnvConfig] = "goldie.yaml"
	}
	return NewLayers(fs, func(k string) (string, bool) { v, ok := e[k]; return v, ok },
		func(string) ([]byte, error) { return []byte(file), nil })
}

// A flag's secret file shadows the environment's inline value rather than
// conflicting with it: the highest layer that sets a secret in either form wins.
func TestLayersASecretFileFlagShadowsTheEnvironmentValue(t *testing.T) {
	l, err := layersFor(t, []string{"--api-token-file", "/run/token"}, map[string]string{EnvAPIToken: "env-value"}, "")
	if err != nil {
		t.Fatal(err)
	}
	lookup := l.Lookup()
	if v, ok := lookup(EnvAPIToken + "_FILE"); !ok || v != "/run/token" {
		t.Errorf("_FILE form = %q, %v, want the flag's path", v, ok)
	}
	if v, ok := lookup(EnvAPIToken); ok {
		t.Errorf("the environment's value %q reached LoadConfig beside the flag's file", v)
	}
	if got := l.Source(EnvAPIToken); got != SourceFlag {
		t.Errorf("source = %q, want flag", got)
	}
}

// A variable that is no setting, such as the *_env variable of a Source, is
// the environment's.
func TestLayersLeaveOtherVariablesToTheEnvironment(t *testing.T) {
	l, err := layersFor(t, nil, map[string]string{"TEAM_TOKEN": "x"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := l.Lookup()("TEAM_TOKEN"); !ok || v != "x" {
		t.Errorf("TEAM_TOKEN = %q, %v", v, ok)
	}
}

// A YAML list of scalars is the comma-separated form of the variable, a
// number and a bool are their text, and a nested value is refused by key.
func TestLayersReadYAMLValuesAsTheVariablesTakeThem(t *testing.T) {
	l, err := layersFor(t, nil, nil, "github_orgs: [acme, other]\ndrain_seconds: 7\nswagger_ui_enabled: false\n")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{EnvGitHubOrgs: "acme,other", EnvDrainSeconds: "7", EnvSwaggerUIEnabled: "false"} {
		if v, ok := l.Lookup()(name); !ok || v != want {
			t.Errorf("%s = %q, %v, want %q", name, v, ok, want)
		}
	}
	_, err = layersFor(t, nil, nil, "poll_interval: {a: b}\n")
	if err == nil || !strings.Contains(err.Error(), "poll_interval") {
		t.Errorf("a mapping for poll_interval: error = %v, want one naming the key", err)
	}
}

// The config file cannot name another file: config is not a key.
func TestLayersRefuseConfigAsAFileKey(t *testing.T) {
	if _, err := layersFor(t, nil, nil, "config: other.yaml\n"); err == nil {
		t.Error("the file named another config file")
	}
}
