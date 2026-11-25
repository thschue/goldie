package api

import (
	"context"
	"net/http"
	"strings"
	"time"

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

// CountItemUses makes the Server count every lookup or get that hits, of an
// article, skill, agent or loop (goldie.item.uses). A nil counter counts
// nothing.
func CountItemUses(c *platform.ItemUseCounter) ServerOption {
	return func(s *Server) { s.itemUses = c }
}

// itemHitKey carries the itemHit a lookup or get fills for record.
type itemHitKey struct{}

// itemHit is the Item a lookup or get answered with: its kind, its canonical
// name (a lookup matches names ignoring case, so the caller's spelling would
// make a series per spelling), whether its Scope is global and the registered
// Project the request resolved for ("" when none).
type itemHit struct {
	kind, name, project string
	global              bool
}

func withItemHit(ctx context.Context) (context.Context, *itemHit) {
	got := &itemHit{}
	return context.WithValue(ctx, itemHitKey{}, got), got
}

// noteItemHit tells record which Item answered a lookup or get. project is
// the registered Project's id from scopeHint, never the one sent.
func noteItemHit(ctx context.Context, kind, name, scope, project string) {
	if got, ok := ctx.Value(itemHitKey{}).(*itemHit); ok {
		*got = itemHit{kind: kind, name: name, project: project, global: scope == engine.ScopeGlobal}
	}
}

// record is the recording point: a strict middleware that records the
// searches and lookups the operations answered. Only an answer the Snapshot
// gave is recorded: a request a guard rejected (401, 426, 429) never reaches
// it, and one the operation refused (400, 503 not_ready) does not match a type
// recorded takes, so no rejected request's query text is kept. The text
// goes to the Activity only, never to a log or a span, and to no metric but
// goldie.skill.gets and goldie.item.uses, which get the canonical name of an Item
// that was found and nothing else.
func (s *Server) record(next contract.StrictHandlerFunc, _ string) contract.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		ctx, got := withItemHit(ctx)
		start := time.Now()
		resp, err := next(ctx, w, r, req)
		latency := time.Since(start)
		if got.kind != "" {
			via := clientVia(r.Header)
			if got.kind == "skill" {
				s.skillGets.Add(ctx, got.name, got.global, via)
			}
			principal, _ := TokenName(ctx)
			s.itemUses.Add(ctx, platform.ItemUse{Kind: got.kind, Name: got.name, Global: got.global,
				Via: via, Project: got.project, Principal: principal})
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
			e.LatencyUS = latency.Microseconds()
			e.Via = clientVia(r.Header)
			if viaSession, _ := ctx.Value(sessionKey{}).(bool); viaSession {
				e.Via = viaUI // the SPA signs in with a cookie; it sends no client header
			}
			e.Project = s.projectSpelling(e.Project)
			if e.Kind == KindSearch && e.Miss {
				if snap := s.provider(); snap != nil {
					e.Nearest = snap.NearestKeywords(e.Query) // the default view: a draft's keyword is never named
				}
			}
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
		return Event{Kind: KindSearch, Query: resp.Query, Project: sentProject(r.Params.Project), Miss: len(resp.Results) == 0, Served: topHit(resp)}, true
	case contract.GetArticle200JSONResponse, contract.GetArticle404JSONResponse:
		r, _ := req.(contract.GetArticleRequestObject)
		found, ok := resp.(contract.GetArticle200JSONResponse)
		return Event{Kind: KindLookup, Query: r.Id, Project: sentProject(r.Params.Project), Miss: !ok, Served: servedRef(ok, "article", found.Id)}, true
	case contract.GetSkill200JSONResponse, contract.GetSkill404JSONResponse:
		r, _ := req.(contract.GetSkillRequestObject)
		found, ok := resp.(contract.GetSkill200JSONResponse)
		return Event{Kind: KindGet, Query: "skill:" + r.Name, Project: sentProject(r.Params.Project), Miss: !ok, Served: servedRef(ok, "skill", found.Name)}, true
	case contract.GetAgent200JSONResponse, contract.GetAgent404JSONResponse:
		r, _ := req.(contract.GetAgentRequestObject)
		found, ok := resp.(contract.GetAgent200JSONResponse)
		return Event{Kind: KindGet, Query: "agent:" + r.Name, Project: sentProject(r.Params.Project), Miss: !ok, Served: servedRef(ok, "agent", found.Name)}, true
	}
	return Event{}, false
}

// topHit is the Reference of a search's first result, or "" without one.
func topHit(resp contract.SearchArticles200JSONResponse) string {
	if len(resp.Results) == 0 {
		return ""
	}
	return servedRef(true, string(resp.Results[0].Kind), resp.Results[0].Id)
}

// servedRef is the Reference <kind>:<name> of the Item served, or "" when
// nothing was (a miss).
func servedRef(found bool, kind, name string) string {
	if !found {
		return ""
	}
	return kind + ":" + name
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
