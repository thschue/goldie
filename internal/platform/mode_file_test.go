package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// GOLDIE_MODE_FILE is read for goldie.mode the way LoadConfig reads it.
func TestTelemetryResourceReadsTheModeFromItsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"GOLDIE_MODE_FILE": path}
	cfg, err := LoadConfig(mapLookup(env))
	if err != nil || cfg.Mode != "api" {
		t.Fatalf("LoadConfig: mode %q, err %v", cfg.Mode, err)
	}
	for signal, res := range newResourceCollector(t).exportOnAllSignals(t, env) {
		if res["goldie.mode"] != "api" {
			t.Errorf("%s: goldie.mode = %q, want api", signal, res["goldie.mode"])
		}
	}
}
