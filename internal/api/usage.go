package api

import (
	"context"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// The usage reads answer how often an Item was used on this replica, from the
// tally beside ItemUseCounter.Add (#900). Each resolves the Item exactly as the
// matching get does, in the caller's Project view, published Items only. An
// Item that does not answer there is the get's 404, so a caller without access
// to an Item's Project learns nothing a miss would not tell. A usage read notes
// no hit, so it is no use of the Item.

// itemUsage is the answer for the Item found in the view: scope is its Scope
// and proj the registered Project the request resolved for.
func (s *Server) itemUsage(kind, name, scope, proj string) contract.ItemUsage {
	v := s.itemUses.Uses(kind, name, proj, scope == engine.ScopeGlobal)
	out := contract.ItemUsage{Source: "this-replica", Since: s.itemUses.Since().UTC()}
	out.Uses.Cli, out.Uses.Mcp, out.Uses.Other = v.CLI, v.MCP, v.Other
	return out
}

// GetArticleUsage answers GET /api/v1/articles/{id}/usage.
func (s *Server) GetArticleUsage(_ context.Context, req contract.GetArticleUsageRequestObject) (contract.GetArticleUsageResponseObject, error) {
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetArticleUsage400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetArticleUsage503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, _ := scopeHint(snap, project)
	a, found := snap.ArticleIn(proj, req.Id, engine.ReadOptions{})
	if !found {
		return contract.GetArticleUsage404JSONResponse(envelope("not_found", "no such article", contract.ArticleMissHint)), nil
	}
	return contract.GetArticleUsage200JSONResponse(s.itemUsage(engine.KindArticle, a.ID, a.Scope, proj)), nil
}

// GetSkillUsage answers GET /api/v1/skills/{name}/usage.
func (s *Server) GetSkillUsage(_ context.Context, req contract.GetSkillUsageRequestObject) (contract.GetSkillUsageResponseObject, error) {
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetSkillUsage400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetSkillUsage503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, _ := scopeHint(snap, project)
	sk, found := snap.SkillIn(proj, req.Name, engine.ReadOptions{})
	if !found {
		return contract.GetSkillUsage404JSONResponse(noSuchSkill), nil
	}
	return contract.GetSkillUsage200JSONResponse(s.itemUsage(engine.KindSkill, sk.Name, sk.Scope, proj)), nil
}

// GetAgentUsage answers GET /api/v1/agents/{name}/usage.
func (s *Server) GetAgentUsage(_ context.Context, req contract.GetAgentUsageRequestObject) (contract.GetAgentUsageResponseObject, error) {
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetAgentUsage400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetAgentUsage503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, _ := scopeHint(snap, project)
	a, found := snap.AgentIn(proj, req.Name, engine.ReadOptions{})
	if !found {
		return contract.GetAgentUsage404JSONResponse(noSuchAgent), nil
	}
	return contract.GetAgentUsage200JSONResponse(s.itemUsage(engine.KindAgent, a.Name, a.Scope, proj)), nil
}

// GetLoopUsage answers GET /api/v1/loops/{name}/usage.
func (s *Server) GetLoopUsage(_ context.Context, req contract.GetLoopUsageRequestObject) (contract.GetLoopUsageResponseObject, error) {
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetLoopUsage400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetLoopUsage503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, _ := scopeHint(snap, project)
	l, found := snap.LoopIn(proj, req.Name, engine.ReadOptions{})
	if !found {
		return contract.GetLoopUsage404JSONResponse(noSuchLoop), nil
	}
	return contract.GetLoopUsage200JSONResponse(s.itemUsage(engine.KindLoop, l.Name, l.Scope, proj)), nil
}
