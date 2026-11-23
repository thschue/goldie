package api

import (
	"context"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// SyncControl is what the sync operations need of the Source's Syncer: its
// status, and a way to start a poll now. Trigger reports whether it started
// one, and ready false when it cannot poll at all (no Source bound, or the
// server stopping), which is not the same as busy.
type SyncControl interface {
	Status() engine.SyncStatus
	Trigger() (started, ready bool)
}

// WithSync makes the Server answer the sync operations from c. Without it GET
// /api/v1/sync lists no Source and POST answers 503 not_ready.
func WithSync(c SyncControl) ServerOption { return func(s *Server) { s.sync = c } }

// syncRunning is the 429 envelope for a trigger while a poll runs.
var syncRunning = envelope("sync_running", "a poll is already running",
	"Read GET /api/v1/sync and trigger again when last_poll has moved.")

// sourceStatuses is the per-Source status the contract sends: one entry for
// the server's one Source, with a time or text left out when it has none.
func (s *Server) sourceStatuses() []contract.SourceSyncStatus {
	if s.sync == nil {
		return []contract.SourceSyncStatus{}
	}
	st := s.sync.Status()
	out := contract.SourceSyncStatus{}
	if st.Revision != "" {
		out.Revision = &st.Revision
	}
	if !st.LastPoll.IsZero() {
		out.LastPoll = &st.LastPoll
	}
	if !st.LastSuccess.IsZero() {
		out.LastSuccess = &st.LastSuccess
	}
	if st.LastError != "" {
		out.LastError = &st.LastError
	}
	return []contract.SourceSyncStatus{out}
}

// PostSync answers POST /api/v1/sync, for operator tokens only (the app guards
// the path): it starts one poll and answers at once, or 429 while one runs.
func (s *Server) PostSync(context.Context, contract.PostSyncRequestObject) (contract.PostSyncResponseObject, error) {
	if s.sync == nil {
		return contract.PostSync503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	started, ready := s.sync.Trigger()
	if !ready {
		return contract.PostSync503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	if !started {
		return contract.PostSync429JSONResponse{Body: syncRunning}, nil
	}
	return contract.PostSync202JSONResponse{Hint: "A poll was started. Read GET /api/v1/sync to see its outcome."}, nil
}
