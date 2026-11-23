package api

import (
	"bytes"
	"context"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// noSuchSkill and noSuchAgent are the 404 envelopes for a name that answers
// nothing in the view. They echo nothing of the request.
var (
	noSuchSkill = envelope("not_found", "no such skill",
		"List the skills, then use a name from the list.")
	noSuchAgent = envelope("not_found", "no such agent",
		"List the agents, then use a name from the list.")
)

// skillAnswer is an engine skill as the contract sends it, files already
// sorted by the engine.
func skillAnswer(sk engine.Skill) contract.Skill {
	files := make([]contract.SkillFile, 0, len(sk.Files))
	for _, f := range sk.Files {
		files = append(files, contract.SkillFile{Path: f, Size: sk.Sizes[f]})
	}
	return contract.Skill{Name: sk.Name, Files: files}
}

// ListSkills answers GET /api/v1/skills. A draft skill is listed only with
// drafts=true, an operator-only parameter as for every read. Each entry also
// carries the fields of a skill card (the single get does not).
func (s *Server) ListSkills(ctx context.Context, req contract.ListSkillsRequestObject) (contract.ListSkillsResponseObject, error) {
	opts, allowed := readOptions(ctx, req.Params.Drafts)
	if !allowed {
		return contract.ListSkills403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.ListSkills400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.ListSkills503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	res := contract.ListSkills200JSONResponse{Skills: []contract.Skill{}, Hint: hint}
	for _, sk := range snap.Skills(proj, opts) {
		ans := skillAnswer(sk)
		ans.Scope = &sk.Scope
		ans.RequiresSync = &sk.RequiresSync
		if sk.Status != "" {
			ans.Status = &sk.Status
		}
		if sk.Description != "" {
			ans.Description = &sk.Description
		}
		res.Skills = append(res.Skills, ans)
	}
	return res, nil
}

// GetSkill answers GET /api/v1/skills/{name}. The name is looked up as a map
// key, never joined to a path.
func (s *Server) GetSkill(ctx context.Context, req contract.GetSkillRequestObject) (contract.GetSkillResponseObject, error) {
	opts, allowed := readOptions(ctx, req.Params.Drafts)
	if !allowed {
		return contract.GetSkill403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetSkill400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetSkill503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	sk, found := snap.SkillIn(proj, req.Name, opts)
	if !found {
		return contract.GetSkill404JSONResponse(noSuchSkill), nil
	}
	noteSkillHit(ctx, sk)
	res := skillAnswer(sk)
	res.Hint = hint
	return contract.GetSkill200JSONResponse(res), nil
}

// ListAgents answers GET /api/v1/agents.
func (s *Server) ListAgents(ctx context.Context, req contract.ListAgentsRequestObject) (contract.ListAgentsResponseObject, error) {
	opts, allowed := readOptions(ctx, req.Params.Drafts)
	if !allowed {
		return contract.ListAgents403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.ListAgents400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.ListAgents503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	res := contract.ListAgents200JSONResponse{Agents: []contract.Agent{}, Hint: hint}
	for _, a := range snap.Agents(proj, opts) {
		card := contract.Agent{Name: a.Name, Description: a.Description, Scope: &a.Scope}
		if a.Status != "" {
			card.Status = &a.Status
		}
		res.Agents = append(res.Agents, card)
	}
	return res, nil
}

// GetAgent answers GET /api/v1/agents/{name}.
func (s *Server) GetAgent(ctx context.Context, req contract.GetAgentRequestObject) (contract.GetAgentResponseObject, error) {
	opts, allowed := readOptions(ctx, req.Params.Drafts)
	if !allowed {
		return contract.GetAgent403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetAgent400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetAgent503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	a, found := snap.AgentIn(proj, req.Name, opts)
	if !found {
		return contract.GetAgent404JSONResponse(noSuchAgent), nil
	}
	return contract.GetAgent200JSONResponse{Name: a.Name, Description: a.Description, Hint: hint}, nil
}

// badTarget is the 400 envelope for a missing or unknown target. It names the
// valid ones and echoes nothing of the request.
var badTarget = envelope("bad_request", "invalid target",
	"Pass target as claude (for CLAUDE.md) or agents (for AGENTS.md).")

// WithInstructionVars sets the variables of the instruction blocks the Server
// renders: {{mcp_name}} is GOLDIE_MCP_NAME and {{cli}} is goldie. Without it
// both are goldie.
func WithInstructionVars(v engine.InstructionVars) ServerOption {
	return func(s *Server) { s.instructionVars = v }
}

// GetInstructions answers GET /api/v1/instructions. The target is checked
// against the two the engine knows before anything else, so it never reaches a
// lookup or the answer.
func (s *Server) GetInstructions(_ context.Context, req contract.GetInstructionsRequestObject) (contract.GetInstructionsResponseObject, error) {
	if !engine.ValidTarget(req.Params.Target) {
		return contract.GetInstructions400JSONResponse(badTarget), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetInstructions400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetInstructions503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	block, digest := snap.Instructions(proj, req.Params.Target, s.instructionVars)
	return contract.GetInstructions200JSONResponse{Block: block, Digest: digest, Hint: hint}, nil
}

// noSuchSkillFile is the 404 envelope for a skill or a path that answers
// nothing, so a name that is no skill and a path that leaves the bundle look
// alike. It echoes nothing of the request.
var noSuchSkillFile = envelope("not_found", "no such skill file",
	"List the skills, read the one you want, then use a path from its files list.")

// GetSkillFile answers GET /api/v1/skills/{name}/files/{path}: the file's
// bytes as written, under the one content type the contract declares and
// nosniff, whatever the file's name or content. The name and path are looked
// up as map keys in the engine, never joined to a filesystem path (ADR 0016).
func (s *Server) GetSkillFile(ctx context.Context, req contract.GetSkillFileRequestObject) (contract.GetSkillFileResponseObject, error) {
	opts, allowed := readOptions(ctx, req.Params.Drafts)
	if !allowed {
		return contract.GetSkillFile403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetSkillFile400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetSkillFile503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, _ := scopeHint(snap, project)
	raw, err := snap.SkillFile(proj, req.Name, req.Path, opts)
	if err != nil {
		return contract.GetSkillFile404JSONResponse(noSuchSkillFile), nil
	}
	return contract.GetSkillFile200ApplicationoctetStreamResponse{Body: bytes.NewReader(raw), ContentLength: int64(len(raw))}, nil
}
