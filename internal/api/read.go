package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// maxQueryBytes bounds the search words, as the spec's maxLength on q says.
const maxQueryBytes = 1024

// badSearch is the 400 envelope for missing or invalid search words, whether
// the generated router or SearchArticles rejects them.
var badSearch = envelope("bad_request", "missing or invalid search words",
	"Pass one q parameter with 1 to 1024 bytes of search words.")

// badProject is the 400 envelope for a project parameter that is not a ProjectId.
var badProject = envelope("bad_request", "invalid project",
	"Pass project as host/org/repo, for example github.com/acme/shop. Leave it out to get global guidance only.")

// checkedProject is a project parameter validProject has passed: a ProjectId,
// or "" when the request named none. Only validProject makes one from a
// request, so an unchecked id cannot reach the hint text.
type checkedProject string

// unregisteredHint tells a caller that named a Project this server does not
// hold that it got the global answer (the Project entry in CONTEXT.md), and
// how to register it. The id is a checked ProjectId, so it holds no markup or
// whitespace.
func unregisteredHint(id checkedProject) string {
	return "The Project " + string(id) + " is not registered on this server, so this is global guidance only. " +
		"To register it, add a projects/" + string(id) + "/ directory to one of goldie's Sources, usually through the platform team."
}

// validProject returns the optional project parameter as a checkedProject
// ("" when absent) and whether it is absent or a ProjectId. It needs no
// Snapshot, so it runs before the not-ready check.
func validProject(project *contract.ProjectId) (checkedProject, bool) {
	if project == nil {
		return "", true
	}
	if !contract.ValidProjectID(*project) {
		return "", false
	}
	return checkedProject(*project), true
}

// scopeHint resolves the project validProject checked. It returns the Project
// to resolve for and the hint when it is not registered.
func scopeHint(snap *engine.Snapshot, project checkedProject) (proj string, hint *string) {
	if project == "" {
		return "", nil
	}
	if scope, registered := snap.Project(string(project)); registered {
		return scope, nil
	}
	h := unregisteredHint(project)
	return "", &h
}

// forbiddenDrafts is the 403 envelope for drafts asked for by a token that is
// not an operator. It echoes nothing of the request.
var forbiddenDrafts = envelope("forbidden", "the API token's role does not allow drafts",
	"Leave out drafts to read the published Items; asking for drafts=true needs a token with the operator role.")

// readOptions is the engine view the drafts parameter asks for, and false when
// the caller's Role may not ask for it: drafts=true needs an operator token,
// and a request without a Role (no Require guard) gets no drafts. It needs no
// Snapshot, so it runs before the not-ready check: a reader's drafts=true can
// never succeed (ADR 0014).
func readOptions(ctx context.Context, drafts *bool) (engine.ReadOptions, bool) {
	if drafts == nil || !*drafts {
		return engine.ReadOptions{}, true
	}
	if role, ok := TokenRole(ctx); !ok || role < RoleOperator {
		return engine.ReadOptions{}, false
	}
	return engine.ReadOptions{Drafts: true}, true
}

// wireStatus maps each engine status a client may see to its ItemStatus.
// Published is not on the wire, and a status missing here is never sent.
var wireStatus = map[string]contract.ItemStatus{
	engine.StatusDeprecated: contract.ItemStatusDeprecated,
	engine.StatusDraft:      contract.ItemStatusDraft,
}

// lifecycle is an Item's status and replaced_by as the contract sends them: a
// published Item, or one whose status has no wire value, carries neither, so
// its answer stays what it was (ADR 0011), and replaced_by is absent when it
// is not set.
func lifecycle(status, replacedBy string) (*contract.ItemStatus, *string) {
	s, ok := wireStatus[status]
	if !ok {
		return nil, nil
	}
	if replacedBy == "" {
		return &s, nil
	}
	return &s, &replacedBy
}

// searchPattern is the pattern the generated router registers the search
// operation under; the mux reports it as the request's Pattern.
const searchPattern = http.MethodGet + " /api/v1/search"

// notReady is the 503 envelope for a read before the first successful load.
// Answering it keeps a direct caller from reading an empty result as "no
// matches" (ADR 0014).
var notReady = envelope("not_ready", "content is still loading",
	"The server has not loaded its content yet. Wait a few seconds and retry.")

// Server implements contract.StrictServerInterface over the current Snapshot,
// read through the provider once per request. The generated router decodes the
// parameters; these methods return the spec's response types. It carries no
// auth: the caller wraps /api/ in RequireToken.
type Server struct {
	provider func() *engine.Snapshot
	specJSON []byte
	activity *Activity // see RecordInto

	keepalive   time.Duration // see KeepStreamsAlive
	streamsDone chan struct{} // closed by EndStreams
	endOnce     sync.Once
}

var _ contract.StrictServerInterface = (*Server)(nil)

// NewServer returns the Server that reads provider() on every request. A nil
// result means no load has succeeded yet, and the read answers 503 not_ready.
func NewServer(provider func() *engine.Snapshot) (*Server, error) {
	specJSON, err := contract.SpecJSON()
	if err != nil {
		return nil, err
	}
	return &Server{provider: provider, specJSON: specJSON,
		keepalive: DefaultActivityKeepalive, streamsDone: make(chan struct{})}, nil
}

// Register registers the spec's operations on m through the generated router,
// as "METHOD path" patterns. Every response carries
// X-Content-Type-Options: nosniff, parameters the router cannot decode are the
// 400 envelope, a response that cannot be written is the 500 envelope, and
// every answered search and lookup is recorded (RecordInto). HEAD on the
// activity stream sends its headers and ends (headOnly).
func (s *Server) Register(m contract.ServeMux) {
	strict := contract.NewStrictHandlerWithOptions(s, []contract.StrictMiddlewareFunc{nosniff, s.record, headOnly},
		contract.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  requestError,
			ResponseErrorHandlerFunc: responseError,
		})
	contract.HandlerWithOptions(strict, contract.StdHTTPServerOptions{
		BaseRouter:       m,
		ErrorHandlerFunc: requestError,
	})
}

// SearchArticles answers GET /api/v1/search.
func (s *Server) SearchArticles(ctx context.Context, req contract.SearchArticlesRequestObject) (contract.SearchArticlesResponseObject, error) {
	// A request that can never succeed is a 403 or 400 before and after the
	// first load, so a caller is not told to retry it.
	opts, allowed := readOptions(ctx, req.Params.Drafts)
	if !allowed {
		return contract.SearchArticles403JSONResponse(forbiddenDrafts), nil
	}
	q := req.Params.Q
	if strings.TrimSpace(q) == "" || len(q) > maxQueryBytes {
		return contract.SearchArticles400JSONResponse(badSearch), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.SearchArticles400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.SearchArticles503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	res := contract.SearchArticles200JSONResponse{Query: q, Results: []contract.SearchHit{}, Hint: hint}
	for _, h := range snap.SearchIn(proj, q, opts) {
		status, replacedBy := lifecycle(h.Status, h.ReplacedBy)
		res.Results = append(res.Results, contract.SearchHit{
			Id:         h.ID,
			Name:       h.Name,
			Kind:       h.Kind,
			Scope:      h.Scope,
			Keywords:   list(h.Keywords),
			Score:      h.Score,
			Matched:    list(h.Matched),
			Status:     status,
			ReplacedBy: replacedBy,
		})
	}
	return res, nil
}

// GetArticle answers GET /api/v1/articles/{id}. The router hands over the id
// decoded, so a nested id sent as one %2F-encoded segment keeps its "/"
// (ADR 0016).
func (s *Server) GetArticle(ctx context.Context, req contract.GetArticleRequestObject) (contract.GetArticleResponseObject, error) {
	opts, allowed := readOptions(ctx, req.Params.Drafts)
	if !allowed {
		return contract.GetArticle403JSONResponse(forbiddenDrafts), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetArticle400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetArticle503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	a, ok := snap.ArticleIn(proj, req.Id, opts)
	if !ok {
		return contract.GetArticle404JSONResponse(envelope("not_found", "no such article",
			"Search for the article with the search operation, then use an id from its results.")), nil
	}
	status, replacedBy := lifecycle(a.Status, a.ReplacedBy)
	return contract.GetArticle200JSONResponse{
		Id:         a.ID,
		Name:       a.Name,
		Keywords:   list(a.Keywords),
		Content:    a.Content,
		Hint:       hint,
		Status:     status,
		ReplacedBy: replacedBy,
	}, nil
}

// ListProjects answers GET /api/v1/projects.
func (s *Server) ListProjects(context.Context, contract.ListProjectsRequestObject) (contract.ListProjectsResponseObject, error) {
	snap := s.provider()
	if snap == nil {
		return contract.ListProjects503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	return contract.ListProjects200JSONResponse{Projects: list(snap.Projects())}, nil
}

// GetOpenAPIYAML answers GET /openapi.yaml with the embedded document.
func (s *Server) GetOpenAPIYAML(context.Context, contract.GetOpenAPIYAMLRequestObject) (contract.GetOpenAPIYAMLResponseObject, error) {
	return specDocument{"application/yaml", contract.SpecYAML()}, nil
}

// GetOpenAPIJSON answers GET /openapi.json with the document converted once
// at startup.
func (s *Server) GetOpenAPIJSON(context.Context, contract.GetOpenAPIJSONRequestObject) (contract.GetOpenAPIJSONResponseObject, error) {
	return specDocument{"application/json", s.specJSON}, nil
}

// specDocument is a 200 response with a fixed body. The generated response
// types would re-encode the JSON document and need a reader for the YAML one;
// this serves the bytes as they are.
type specDocument struct {
	contentType string
	body        []byte
}

func (d specDocument) VisitGetOpenAPIYAMLResponse(w http.ResponseWriter) error {
	return writeFixed(w, d.contentType, d.body, true)
}

func (d specDocument) VisitGetOpenAPIJSONResponse(w http.ResponseWriter) error {
	return writeFixed(w, d.contentType, d.body, true)
}

// StaticGet serves a fixed body with X-Content-Type-Options: nosniff. The
// router sends it GET and HEAD only, and HEAD gets no body.
func StaticGet(contentType string, body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = writeFixed(w, contentType, body, r.Method == http.MethodGet)
	})
}

// writeFixed writes a 200 with contentType and nosniff, and body when withBody
// is set.
func writeFixed(w http.ResponseWriter, contentType string, body []byte, withBody bool) error {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if !withBody {
		return nil
	}
	_, err := w.Write(body)
	return err
}

// nosniff sets X-Content-Type-Options on every operation's response.
func nosniff(next contract.StrictHandlerFunc, _ string) contract.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		return next(ctx, w, r, req)
	}
}

// requestError answers a request whose parameters the generated router cannot
// decode with the 400 envelope; Register hands it to the generated router and
// the strict handler. A missing or repeated q on the search operation is the
// envelope SearchArticles gives for blank search words. The error is not
// echoed: it quotes the request.
func requestError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Pattern == searchPattern && paramName(err) == "q" {
		writeJSON(w, http.StatusBadRequest, badSearch)
		return
	}
	WriteError(w, http.StatusBadRequest, "bad_request", "invalid request parameters",
		"Check the request parameters against the OpenAPI document.")
}

// paramName is the parameter a generated decoding error names, or "".
func paramName(err error) string {
	var (
		required *contract.RequiredParamError
		format   *contract.InvalidParamFormatError
	)
	switch {
	case errors.As(err, &required):
		return required.ParamName
	case errors.As(err, &format):
		return format.ParamName
	}
	return ""
}

// responseError answers a response the strict handler could not produce or
// encode with the 500 envelope, without the error's text.
func responseError(w http.ResponseWriter, _ *http.Request, _ error) {
	WriteError(w, http.StatusInternalServerError, "internal_error", "the server could not answer",
		"Retry the request; if it keeps failing, ask the operator to check the server logs.")
}

// list makes a nil slice an empty one, so it encodes as [] as the spec requires.
func list(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// envelope builds the error envelope from the generated contract types
// (ADR 0014).
func envelope(code, message, hint string) contract.ErrorEnvelope {
	return contract.ErrorEnvelope{Error: contract.Error{
		Code:    code,
		Message: message,
		Details: contract.ErrorDetails{Hint: hint},
	}}
}

// WriteError writes the error envelope {"error":{code,message,details}}
// (ADR 0014). hint is the next-step hint in details; it must not echo the
// request or contain addresses, paths or stack traces.
func WriteError(w http.ResponseWriter, status int, code, message, hint string) {
	writeJSON(w, status, envelope(code, message, hint))
}
