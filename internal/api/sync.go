package api

import (
	"context"
	"strings"

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

// SourceSync is one Source's name and status, as a SourceSyncControl lists it.
type SourceSync struct {
	Name   string
	Status engine.SyncStatus
}

// SourceSyncControl is a SyncControl over several named Sources. Sources lists
// them in preference order. TriggerSource polls the named one, or every one
// for an empty name: found is false for a name that is no Source, started
// false while its poll runs, ready false when nothing can poll.
type SourceSyncControl interface {
	SyncControl
	Sources() []SourceSync
	TriggerSource(name string) (started, ready, found bool)
}

// maxShadowed caps the shadowed list of one Source in the status.
const maxShadowed = 100

// WithSync makes the Server answer the sync operations from c. Without it GET
// /api/v1/sync lists no Source and POST answers 503 not_ready.
func WithSync(c SyncControl) ServerOption { return func(s *Server) { s.sync = c } }

// syncRunning is the 429 envelope for a trigger while a poll runs.
var syncRunning = envelope("sync_running", "a poll is already running",
	"Read GET /api/v1/sync and trigger again when last_poll has moved.")

// sourceUnknown is the 404 envelope for a source parameter naming no Source.
// It echoes nothing of the name.
var sourceUnknown = envelope("not_found", "no such Source",
	"read GET /api/v1/sync for the configured Source names")

// sources is the control's Sources: its own list, or the one Source of a
// control that is not a SourceSyncControl.
func (s *Server) sources() []SourceSync {
	if m, ok := s.sync.(SourceSyncControl); ok {
		return m.Sources()
	}
	return []SourceSync{{Name: "source", Status: s.sync.Status()}}
}

// sourceStatuses is the per-Source status the contract sends, in preference
// order, with a time or text left out when a Source has none.
func (s *Server) sourceStatuses() []contract.SourceSyncStatus {
	out := []contract.SourceSyncStatus{}
	if s.sync == nil {
		return out
	}
	for i, src := range s.sources() {
		out = append(out, sourceStatus(i+1, src))
	}
	return out
}

// sourceStatus is the contract's status of src, which stands at preference.
func sourceStatus(preference int, src SourceSync) contract.SourceSyncStatus {
	st := src.Status
	shadowed := []contract.ShadowedItem{}
	for _, sh := range st.Shadowed[:min(len(st.Shadowed), maxShadowed)] {
		shadowed = append(shadowed, shadowedItem(sh))
	}
	out := contract.SourceSyncStatus{Name: &src.Name, Preference: &preference, Shadowed: &shadowed}
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
	return out
}

// shadowedItem is the contract's form of sh: its Item split into kind and ID,
// and the Project named in project when the Scope is a Project's.
func shadowedItem(sh engine.Shadowed) contract.ShadowedItem {
	kind, id, _ := strings.Cut(sh.Item, ":")
	out := contract.ShadowedItem{Kind: kind, Id: id, Scope: sh.Scope, ShadowedBy: contract.ShadowedBy{Source: sh.By, Scope: sh.Scope}}
	if sh.Scope != engine.ScopeGlobal && sh.Scope != engine.ScopeLocal {
		project := sh.Scope
		out.Project, out.ShadowedBy.Project = &project, &project
	}
	return out
}

// PostSync answers POST /api/v1/sync, for operator tokens only (the app guards
// the path): it starts a poll of every Source, or of the one named by source,
// and answers at once, 429 while that poll runs, or 404 for an unknown name.
func (s *Server) PostSync(_ context.Context, req contract.PostSyncRequestObject) (contract.PostSyncResponseObject, error) {
	if s.sync == nil {
		return contract.PostSync503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	started, ready, found := s.trigger(deref(req.Params.Source))
	if !found {
		return contract.PostSync404JSONResponse(sourceUnknown), nil
	}
	if !ready {
		return contract.PostSync503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	if !started {
		return contract.PostSync429JSONResponse{Body: syncRunning}, nil
	}
	return contract.PostSync202JSONResponse{Hint: "A poll was started. Read GET /api/v1/sync to see its outcome."}, nil
}

// trigger starts the poll of the named Source, or of every one without a name.
// found is false for a name that is no Source of this server.
func (s *Server) trigger(name string) (started, ready, found bool) {
	if m, ok := s.sync.(SourceSyncControl); ok {
		return m.TriggerSource(name)
	}
	if name != "" {
		return false, true, false
	}
	started, ready = s.sync.Trigger()
	return started, ready, true
}
