package main

import (
	"strings"
	"testing"
)

func TestCLIStatusJSONCarriesTheUnregisteredHint(t *testing.T) {
	srv := newScopedServer(t)
	m := statusJSON(t, t.TempDir(), "--addr", srv.URL, "--token", cliToken, "--project", unregisteredProject)
	if h, _ := m["hint"].(string); !strings.Contains(strings.ToLower(h), "not registered") {
		t.Errorf("status --json hint = %v", m["hint"])
	}
	m = statusJSON(t, t.TempDir(), "--addr", srv.URL, "--token", cliToken, "--project", registeredProject)
	if _, ok := m["hint"]; ok {
		t.Errorf("registered Project has a hint: %v", m["hint"])
	}
}

func TestCLIInvalidEnvProjectBlocksSearchBeforeAnyRequest(t *testing.T) {
	const secret = "ghp_s3cretTokenValue"
	srv := newScopedServer(t)
	_, err := runScoped(t, t.TempDir(), "user:"+secret+"@host", scopedArgs(srv, "search", "kubernetes")...)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("err = %v, want an error without the credential", err)
	}
	if got := srv.projects(); len(got) != 0 {
		t.Errorf("search sent %q despite a broken GOLDIE_PROJECT", got)
	}
}
