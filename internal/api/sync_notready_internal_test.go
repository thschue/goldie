package api

import (
	"context"
	"testing"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

type stubSync struct{ started, ready bool }

func (stubSync) Status() engine.SyncStatus { return engine.SyncStatus{} }
func (s stubSync) Trigger() (bool, bool)   { return s.started, s.ready }

// POST /api/v1/sync tells "not ready" (no Syncer bound, or the server has not
// started) apart from "busy": 503 for the first, 429 for the second.
func TestPostSyncNotReadyIsNotBusy(t *testing.T) {
	cases := map[string]struct {
		sync SyncControl
		want string
	}{
		"no control bound":  {nil, "503"},
		"control not ready": {stubSync{started: false, ready: false}, "503"},
		"poll running":      {stubSync{started: false, ready: true}, "429"},
		"poll started":      {stubSync{started: true, ready: true}, "202"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := NewServer(func() *engine.Snapshot { return nil })
			if err != nil {
				t.Fatal(err)
			}
			s.sync = c.sync
			got, _ := s.PostSync(context.Background(), contract.PostSyncRequestObject{})
			var code string
			switch got.(type) {
			case contract.PostSync503JSONResponse:
				code = "503"
			case contract.PostSync429JSONResponse:
				code = "429"
			case contract.PostSync202JSONResponse:
				code = "202"
			}
			if code != c.want {
				t.Errorf("answered %T, want %s", got, c.want)
			}
		})
	}
}
