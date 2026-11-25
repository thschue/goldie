package api

import (
	"context"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// noSuchLoop is the 404 envelope for a name that answers nothing in the view.
// It echoes nothing of the request.
var noSuchLoop = envelope("not_found", "no such loop",
	contract.LoopMissHint)

// loopAnswer is an engine loop as the contract sends it. The prompt is only
// sent by a get.
func loopAnswer(l engine.Loop, withContent bool) contract.Loop {
	refs := func(in []engine.LoopRef) []contract.LoopRef {
		out := make([]contract.LoopRef, 0, len(in))
		for _, r := range in {
			out = append(out, contract.LoopRef{Name: r.Name, Scope: r.Scope})
		}
		return out
	}
	ans := contract.Loop{
		Name: l.Name, Description: l.Description, Dynamic: l.Dynamic, Stop: l.Stop, Digest: l.Digest, Scope: &l.Scope,
		Closure: contract.LoopClosure{Agents: refs(l.Agents), Skills: refs(l.Skills)},
	}
	if l.Interval != "" {
		ans.Interval = &l.Interval
	}
	if withContent {
		ans.Content = &l.Content
	}
	return ans
}

// ListLoops answers GET /api/v1/loops, as ListAgents answers the agents.
func (s *Server) ListLoops(ctx context.Context, req contract.ListLoopsRequestObject) (contract.ListLoopsResponseObject, error) {
	opts, allowed := readOptions(ctx, req.Params.Drafts)
	if !allowed {
		return contract.ListLoops403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.ListLoops400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.ListLoops503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	res := contract.ListLoops200JSONResponse{Loops: []contract.Loop{}, Hint: hint}
	for _, l := range snap.Loops(proj, opts) {
		res.Loops = append(res.Loops, loopAnswer(l, false))
	}
	return res, nil
}

// GetLoop answers GET /api/v1/loops/{name}, as GetAgent answers an agent.
func (s *Server) GetLoop(ctx context.Context, req contract.GetLoopRequestObject) (contract.GetLoopResponseObject, error) {
	opts, allowed := readOptions(ctx, req.Params.Drafts)
	if !allowed {
		return contract.GetLoop403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetLoop400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetLoop503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	l, found := snap.LoopIn(proj, req.Name, opts)
	if !found {
		return contract.GetLoop404JSONResponse(noSuchLoop), nil
	}
	noteItemHit(ctx, "loop", l.Name, l.Scope, proj)
	res := loopAnswer(l, true)
	res.Hint = hint
	return contract.GetLoop200JSONResponse(res), nil
}
