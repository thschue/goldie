package main

import (
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"
)

// #257: the binary registers the dir Source with the admin readiness registry.
// /readyz lists source:dir as ok (PC-02), shows only name and status, and
// never echoes the content path. Content that will not load still stops
// startup (TestPC04ServerNeedsTokenAndContent).
func TestBinaryReadyzListsSourceDirOk(t *testing.T) {
	p := startServer(t, "GOLDIE_DRAIN_SECONDS=0")
	defer func() { _ = p.cmd.Process.Signal(syscall.SIGTERM); _ = p.waitExit(t, 5*time.Second) }()
	code, body := httpGet(t, "http://"+p.admin+"/readyz")
	if code != 200 {
		t.Fatalf("/readyz = %d: %s", code, body)
	}
	var got struct {
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("/readyz is not JSON: %v: %s", err, body)
	}
	if got.Status != "ready" || len(got.Checks) != 1 || got.Checks[0].Name != "source:dir" || got.Checks[0].Status != "ok" {
		t.Fatalf("/readyz = %s, want ready with exactly source:dir ok", body)
	}
	if strings.Contains(body, contentDir(t)) || strings.Contains(body, "rror") {
		t.Errorf("/readyz echoes a path or an error: %s", body)
	}
}
