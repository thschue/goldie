package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// #858: GOLDIE_CONFIG set with --dev stops the server with the --dev message,
// as --config does.
func TestServerDevRejectsAConfigFileGivenByVariable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goldieBinary(t), "server", "--dev", contentDir(t))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GOLDIE_CONFIG=" + configFile(t, "poll_interval: 45s\n")}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("--dev with GOLDIE_CONFIG kept running instead of stopping:\n%s", out)
	}
	if err == nil {
		t.Fatalf("--dev with GOLDIE_CONFIG exited zero:\n%s", out)
	}
	msg := strings.ToLower(string(out))
	if !strings.Contains(msg, "--dev") || !strings.Contains(msg, "config file") {
		t.Errorf("the error does not say --dev does not read a config file:\n%s", out)
	}
}
