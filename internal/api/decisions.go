package api

import (
	"context"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// noSuchDecision is the 404 envelope for a name that answers nothing in the
// view. It echoes nothing of the request.
var noSuchDecision = envelope("not_found", "no such decision",
	contract.DecisionMissHint)

// badDecisionStatus is the 400 envelope for a status that is not one the list
// knows. It does not repeat the value.
var badDecisionStatus = envelope("bad_request", "invalid status",
	"Pass status as accepted, proposed, rejected, deprecated, superseded or all. Leave it out for accepted decisions.")

// statusFilter reads the status parameter: the status to keep, "" for all. An
// unknown value is not ok.
func statusFilter(status *string) (keep string, ok bool) {
	if status == nil {
		return engine.StatusAccepted, true
	}
	switch *status {
	case "all":
		return "", true
	case engine.StatusAccepted, engine.StatusProposed, engine.StatusRejected, engine.StatusDeprecated, engine.StatusSuperseded:
		return *status, true
	}
	return "", false
}

// decisionAnswer is an engine decision as the contract sends it. The body and
// the chain are only sent by a get.
func decisionAnswer(d engine.Decision) contract.Decision {
	return contract.Decision{
		Name: d.Name, Title: d.Title, Status: contract.DecisionStatus(d.Status), Date: d.Date, Scope: d.Scope,
		Supersedes: append([]string{}, d.Supersedes...), SupersededBy: d.SupersededBy,
		Keywords: append([]string{}, d.Keywords...),
	}
}

// ListDecisions answers GET /api/v1/decisions, as ListLoops answers the loops.
// The status filter is open to readers; drafts stays operator-only and does
// not widen the list.
func (s *Server) ListDecisions(ctx context.Context, req contract.ListDecisionsRequestObject) (contract.ListDecisionsResponseObject, error) {
	if _, allowed := readOptions(ctx, req.Params.Drafts); !allowed {
		return contract.ListDecisions403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.ListDecisions400JSONResponse(badProject), nil
	}
	keep, ok := statusFilter(req.Params.Status)
	if !ok {
		return contract.ListDecisions400JSONResponse(badDecisionStatus), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.ListDecisions503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	// An accepted list is the engine's default view; any other status reads
	// every status and keeps the one asked for.
	opts := engine.ReadOptions{Drafts: keep != engine.StatusAccepted}
	res := contract.ListDecisions200JSONResponse{Decisions: []contract.Decision{}, Hint: hint}
	for _, d := range snap.Decisions(proj, opts) {
		if keep == "" || d.Status == keep {
			res.Decisions = append(res.Decisions, decisionAnswer(d))
		}
	}
	return res, nil
}

// GetDecision answers GET /api/v1/decisions/{name}, as GetLoop answers a loop.
// A decision of any status is served: a superseded one has a history to show.
func (s *Server) GetDecision(ctx context.Context, req contract.GetDecisionRequestObject) (contract.GetDecisionResponseObject, error) {
	if _, allowed := readOptions(ctx, req.Params.Drafts); !allowed {
		return contract.GetDecision403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetDecision400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetDecision503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	d, found := snap.DecisionIn(proj, req.Name, engine.ReadOptions{Drafts: true})
	chain, _ := snap.DecisionChain(proj, req.Name)
	if !found {
		return contract.GetDecision404JSONResponse(noSuchDecision), nil
	}
	res := decisionAnswer(d)
	res.Content = &d.Content
	entries := make([]contract.DecisionChainEntry, 0, len(chain))
	for _, c := range chain {
		entries = append(entries, contract.DecisionChainEntry{Name: c.Name, Title: c.Title, Status: contract.DecisionChainEntryStatus(c.Status), Scope: c.Scope})
	}
	res.Chain = &entries
	res.Hint = hint
	return contract.GetDecision200JSONResponse(res), nil
}
