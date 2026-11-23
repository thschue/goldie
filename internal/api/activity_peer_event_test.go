package api

import (
	"strings"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/contract"
)

// A peer's event is untrusted: peerEvent cuts every text field to
// maxQueryBytes, so a peer cannot make the merge retain megabytes per event.
func TestPeerEventBoundsEveryTextField(t *testing.T) {
	huge := strings.Repeat("x", 4<<20)
	e := peerEvent(contract.ActivityEvent{
		Id: 7, Time: time.Unix(1, 0), Kind: contract.ActivityEventKind(huge), Query: huge, Token: huge,
		ClientVersion: huge, Project: huge, TraceId: huge, Replica: huge, Late: true,
	})
	for name, got := range map[string]string{"kind": string(e.Kind), "query": e.Query, "token": e.Token,
		"client_version": e.ClientVersion, "project": e.Project, "trace_id": e.TraceID, "replica": e.Replica} {
		if len(got) != maxQueryBytes {
			t.Errorf("%s is %d bytes, want it cut to %d", name, len(got), maxQueryBytes)
		}
	}
	if e.ID != 7 || e.Late {
		t.Errorf("event %+v: want id 7 and Late cleared", e)
	}
}
