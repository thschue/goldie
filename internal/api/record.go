package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// ServerOption configures NewServer.
type ServerOption func(*Server)

// RecordInto makes the Server record every search and lookup it answers in a,
// one Event each. A nil a records nothing.
func RecordInto(a *Activity) ServerOption { return func(s *Server) { s.activity = a } }

// CountResults makes the Server count every search and lookup it answers, by
// hit or miss: searches into the first counter, lookups into the second
// (goldie.searches, goldie.lookups). A nil counter counts nothing.
func CountResults(searches, lookups *platform.ResultCounter) ServerOption {
	return func(s *Server) { s.searches, s.lookups = searches, lookups }
}

// CountSkillGets makes the Server count every skill get that hits
// (goldie.skill.gets). A nil counter counts nothing.
func CountSkillGets(c *platform.SkillGetCounter) ServerOption {
	return func(s *Server) { s.skillGets = c }
}

// skillHitKey carries the skillHit GetSkill fills for record.
type skillHitKey struct{}

// skillHit is the skill a get answered with: its canonical name (SkillIn
// matches names ignoring case, so the caller's spelling would make a series
// per spelling) and whether its Scope is global.
type skillHit struct {
	name   string
	global bool
}

func withSkillHit(ctx context.Context) (context.Context, *skillHit) {
	got := &skillHit{}
	return context.WithValue(ctx, skillHitKey{}, got), got
}

// noteSkillHit tells record which skill answered a get.
func noteSkillHit(ctx context.Context, sk engine.Skill) {
	if got, ok := ctx.Value(skillHitKey{}).(*skillHit); ok {
		got.name, got.global = sk.Name, sk.Scope == engine.ScopeGlobal
	}
}

// record is the recording point: a strict middleware that records the
// searches and lookups the operations answered. Only an answer the Snapshot
// gave is recorded: a request a guard rejected (401, 426, 429) never reaches
// it, and one the operation refused (400, 503 not_ready) does not match a type
// recorded takes, so no rejected request's query text is kept. The text
// goes to the Activity only, never to a log or a span, and to no metric but
// goldie.skill.gets, which gets the canonical name of a skill that was found
// and nothing else.
func (s *Server) record(next contract.StrictHandlerFunc, _ string) contract.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		ctx, got := withSkillHit(ctx)
		resp, err := next(ctx, w, r, req)
		if _, hit := resp.(contract.GetSkill200JSONResponse); hit {
			s.skillGets.Add(ctx, got.name, got.global, clientVia(r.Header))
		}
		if e, ok := recorded(req, resp); ok {
			switch e.Kind {
			case KindSearch:
				s.searches.Add(ctx, e.Miss)
			case KindLookup:
				s.lookups.Add(ctx, e.Miss) // article lookups only; a get of a skill or agent is not one
			}
			e.Token, _ = TokenName(ctx)
			if v, ok := clientVersion(r.Header.Values(contract.ClientHeader)); ok {
				e.ClientVersion = v.String()
			}
			e.TraceID = platform.TraceID(ctx)
			e.Project = s.projectSpelling(e.Project)
			s.activity.Record(e)
		}
		return resp, err
	}
}

// recorded is the Event for an answered search, lookup or get: its words, the
// article id, or skill:<name> or agent:<name> for a get (ADR 0021), the project as sent
// (record stores its spelling), and whether it is a Miss (a search with zero
// results, or a lookup or get answering 404). Any other answer is not
// recorded.
func recorded(req, resp any) (Event, bool) {
	switch resp := resp.(type) {
	case contract.SearchArticles200JSONResponse:
		r, _ := req.(contract.SearchArticlesRequestObject)
		return Event{Kind: KindSearch, Query: resp.Query, Project: sentProject(r.Params.Project), Miss: len(resp.Results) == 0}, true
	case contract.GetArticle200JSONResponse, contract.GetArticle404JSONResponse:
		r, _ := req.(contract.GetArticleRequestObject)
		_, miss := resp.(contract.GetArticle404JSONResponse)
		return Event{Kind: KindLookup, Query: r.Id, Project: sentProject(r.Params.Project), Miss: miss}, true
	case contract.GetSkill200JSONResponse, contract.GetSkill404JSONResponse:
		r, _ := req.(contract.GetSkillRequestObject)
		_, miss := resp.(contract.GetSkill404JSONResponse)
		return Event{Kind: KindGet, Query: "skill:" + r.Name, Project: sentProject(r.Params.Project), Miss: miss}, true
	case contract.GetAgent200JSONResponse, contract.GetAgent404JSONResponse:
		r, _ := req.(contract.GetAgentRequestObject)
		_, miss := resp.(contract.GetAgent404JSONResponse)
		return Event{Kind: KindGet, Query: "agent:" + r.Name, Project: sentProject(r.Params.Project), Miss: miss}, true
	}
	return Event{}, false
}

// projectSpelling is the registered spelling of the Project sent, matched
// ignoring case, or the id lower-cased when it is not registered, so one
// Project's events never split on case (ADR 0020). "" stays "".
func (s *Server) projectSpelling(sent string) string {
	if sent == "" {
		return ""
	}
	if snap := s.provider(); snap != nil {
		if registered, ok := snap.Project(sent); ok {
			return registered
		}
	}
	return strings.ToLower(sent)
}

// sentProject is the project parameter as sent, or "" without one or when
// validProject rejects it, so an unchecked id is never recorded.
func sentProject(p *contract.ProjectId) string {
	project, _ := validProject(p)
	return string(project)
}
