package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/thschue/goldie/internal/contract"
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

// record is the recording point: a strict middleware that records the
// searches and lookups the operations answered. Only an answer the Snapshot
// gave is recorded: a request a guard rejected (401, 426, 429) never reaches
// it, and one the operation refused (400, 503 not_ready) does not match a type
// recorded takes, so no rejected request's query text is kept. The text
// goes to the Activity only, never to a log, a metric or a span.
func (s *Server) record(next contract.StrictHandlerFunc, _ string) contract.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		resp, err := next(ctx, w, r, req)
		if e, ok := recorded(req, resp); ok {
			if e.Kind == KindSearch {
				s.searches.Add(ctx, e.Miss)
			} else {
				s.lookups.Add(ctx, e.Miss)
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
// article id or the skill or agent name asked for, the project as sent (record stores its spelling), and
// whether it is a Miss (a search with zero results, or a lookup answering
// 404). Any other answer is not recorded.
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
		return Event{Kind: KindGet, Query: r.Name, Project: sentProject(r.Params.Project), Miss: miss}, true
	case contract.GetAgent200JSONResponse, contract.GetAgent404JSONResponse:
		r, _ := req.(contract.GetAgentRequestObject)
		_, miss := resp.(contract.GetAgent404JSONResponse)
		return Event{Kind: KindGet, Query: r.Name, Project: sentProject(r.Params.Project), Miss: miss}, true
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
