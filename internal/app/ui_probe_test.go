package app

import (
	"net/http"
	"testing"
)

// The reserved probe path answers the 404 envelope that the OpenAPI document's
// default error response describes, whatever the UI serves at the other paths.
func TestReservedProbePathAnswersTheContractedNotFoundEnvelope(t *testing.T) {
	srv := newServer(t)
	resp, body := get(t, srv.URL+"/livez")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", resp.StatusCode, body)
	}
	assertEnvelope(t, body, "not_found")
}
