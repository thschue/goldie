package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
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

// instructionsPattern is the pattern the generated router registers the
// instructions operation under, for the same reason.
const instructionsPattern = http.MethodGet + " /api/v1/instructions"

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
	provider   func() *engine.Snapshot
	specJSON   []byte
	activity   *Activity               // see RecordInto, nil records nothing
	searches   *platform.ResultCounter // see CountResults, nil counts nothing
	lookups    *platform.ResultCounter
	skillGets  *platform.SkillGetCounter // see CountSkillGets, nil counts nothing
	fed        *Federation               // see FederateActivity, nil serves ?replicas=all as the local stream
	fedOpen    atomic.Int32              // merged streams open, at most MaxFederatedSubscribers
	fedDown    downSet                   // peers already logged as down
	sync       SyncControl               // see WithSync, nil lists no Source
	tokens     *Tokens                   // see ServeSessions, nil: no sign-in
	gh         GitHubSignIn              // see WithGitHub, nil: no sign-in with GitHub
	ghRedirect string                    // the callback URL, from GOLDIE_PUBLIC_URL
	swaggerUI  bool                      // see WithSwaggerUI
	github     bool                      // see WithGitHubSignIn
	grafana    string                    // see WithObservabilityLinks
	traceTmpl  string

	instructionVars engine.InstructionVars // see WithInstructionVars

	keepalive   time.Duration // see KeepStreamsAlive
	streamsDone chan struct{} // closed by EndStreams
	endOnce     sync.Once
}

var _ contract.StrictServerInterface = (*Server)(nil)

// NewServer returns the Server that reads provider() on every request. A nil
// result means no load has succeeded yet, and the read answers 503 not_ready.
func NewServer(provider func() *engine.Snapshot, opts ...ServerOption) (*Server, error) {
	specJSON, err := contract.SpecJSON()
	if err != nil {
		return nil, err
	}
	s := &Server{provider: provider, specJSON: specJSON,
		instructionVars: engine.InstructionVars{MCPName: "goldie", CLI: "goldie"},
		keepalive:       DefaultActivityKeepalive, streamsDone: make(chan struct{})}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Register registers the spec's operations on m through the generated router,
// as "METHOD path" patterns. Every response carries
// X-Content-Type-Options: nosniff, parameters the router cannot decode are the
// 400 envelope, a response that cannot be written is the 500 envelope, and
// every answered search and lookup is recorded when the Server was built with RecordInto. HEAD on the
// activity stream sends its headers and ends (headOnly).
func (s *Server) Register(m contract.ServeMux) {
	strict := contract.NewStrictHandlerWithOptions(s, []contract.StrictMiddlewareFunc{nosniff, s.record, headOnly, s.activityRequest, s.sessionCookies, s.githubFlowRequest},
		contract.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  requestError,
			ResponseErrorHandlerFunc: responseError,
		})
	contract.HandlerWithOptions(strict, contract.StdHTTPServerOptions{
		BaseRouter:       m,
		ErrorHandlerFunc: requestError,
	})
}

// badKind is the 400 envelope for a kind that is none of article, skill,
// agent or decision. It does not echo the value.
var badKind = envelope("bad_request", "invalid kind",
	"Use kind=article, kind=skill, kind=agent or kind=decision, or leave it out to search articles.")

// searchKind is the Kind a search asks for: an article when kind is absent.
func searchKind(k *contract.SearchArticlesParamsKind) (string, bool) {
	if k == nil {
		return engine.KindArticle, true
	}
	switch kind := string(*k); kind {
	case engine.KindArticle, engine.KindSkill, engine.KindAgent, engine.KindDecision:
		return kind, true
	}
	return "", false
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
	kind, ok := searchKind(req.Params.Kind)
	if !ok {
		return contract.SearchArticles400JSONResponse(badKind), nil
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
	for _, h := range snap.SearchKindIn(proj, kind, q, opts) {
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
			Overrides:  new(list(h.Overrides)),
			Enforced:   new(h.Enforced),
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
		if ref, file, hidden := snap.SuppressedIn(proj, req.Id); hidden {
			return contract.GetArticle404JSONResponse(envelope("not_found", "no such article", suppressedHint(project, ref, file))), nil
		}
		return contract.GetArticle404JSONResponse(envelope("not_found", "no such article",
			contract.ArticleMissHint)), nil
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
		Scope:      new(a.Scope),
		Overrides:  new(list(a.Overrides)),
		Enforced:   new(a.Enforced),
	}, nil
}

// suppressedHint says which Suppression hides a global Item from a Project and
// which file sets it. The Project is a checked ProjectId, the reference an
// Item id the load validated and the file is built from validated segments, so
// the text holds no markup or line break.
func suppressedHint(project checkedProject, ref, file string) string {
	return "The Project " + string(project) + " suppresses " + ref + " in " + file +
		". Remove that entry to read the Item, or look it up without the project."
}

// GetProject answers GET /api/v1/projects/{id}. The router hands over the id
// decoded, one %2F-encoded segment (ADR 0016); one that is not a ProjectId is
// a 400 that does not echo it.
func (s *Server) GetProject(_ context.Context, req contract.GetProjectRequestObject) (contract.GetProjectResponseObject, error) {
	if !contract.ValidProjectID(req.Id) {
		return contract.GetProject400JSONResponse(envelope("bad_request", "invalid project",
			"Pass the Project as host/org/repo in one path segment, for example github.com%2Facme%2Fshop.")), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetProject503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	scope, registered := snap.Project(req.Id)
	if !registered {
		return contract.GetProject404JSONResponse(envelope("not_found", "no such Project",
			"List the registered Projects, then use one of them. To register one, add a projects/host/org/repo/ directory to one of goldie's Sources, usually through the platform team.")), nil
	}
	return contract.GetProject200JSONResponse{Id: scope, Suppressions: list(snap.Suppressions(scope))}, nil
}

// GetSync answers GET /api/v1/sync, for operator tokens only (the app guards
// the path): the load's warnings, already escaped and cut by the engine.
func (s *Server) GetSync(context.Context, contract.GetSyncRequestObject) (contract.GetSyncResponseObject, error) {
	snap := s.provider()
	if snap == nil {
		return contract.GetSync503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	warnings := []contract.ContentWarning{}
	for _, w := range snap.Warnings() {
		warnings = append(warnings, contentWarning(w))
	}
	return contract.GetSync200JSONResponse{Warnings: warnings, Sources: s.sourceStatuses()}, nil
}

// contentWarning splits an engine warning, "<file>: <message>", at the first
// ": ". The file is built from validated path segments and holds none. A
// warning without one is all message.
func contentWarning(w string) contract.ContentWarning {
	file, message, ok := strings.Cut(w, ": ")
	if !ok {
		return contract.ContentWarning{Message: w}
	}
	return contract.ContentWarning{File: file, Message: message}
}

// ListProjects answers GET /api/v1/projects.
func (s *Server) ListProjects(context.Context, contract.ListProjectsRequestObject) (contract.ListProjectsResponseObject, error) {
	snap := s.provider()
	if snap == nil {
		return contract.ListProjects503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	return contract.ListProjects200JSONResponse{Projects: list(snap.Projects())}, nil
}

// maxViewBytes is the most a Project's exported view may weigh as JSON: what
// the hardened client reads, so the server never sends a view its own client
// would refuse.
const maxViewBytes = contract.MaxResponseBytes

// viewTooLarge is the 422 envelope for a view over maxViewBytes.
var viewTooLarge = envelope("view_too_large", "the Project's view is too large to export",
	"This Project's content is over the export limit; split it across Projects or Sources.")

// GetProjectItems answers GET /api/v1/projects/{id}/items: the winning
// articles in the Project's view, sorted by id, with their Scope and Enforced
// flag, and never a draft. An unregistered Project gets the global view and
// the usual hint. A view over maxViewBytes is refused whole (422), never cut.
func (s *Server) GetProjectItems(_ context.Context, req contract.GetProjectItemsRequestObject) (contract.GetProjectItemsResponseObject, error) {
	project, ok := validProject(&req.Id)
	if !ok {
		return contract.GetProjectItems400JSONResponse(envelope("bad_request", "invalid project",
			"Pass the Project as host/org/repo in one path segment, for example github.com%2Facme%2Fshop.")), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetProjectItems503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, hint := scopeHint(snap, project)
	view := snap.View(proj)
	// Content alone is a lower bound of the body: refuse before encoding a
	// view that cannot fit.
	size := 0
	for _, a := range view {
		if size += len(a.Content); size > maxViewBytes {
			return contract.GetProjectItems422JSONResponse(viewTooLarge), nil
		}
	}
	res := contract.ProjectView{Items: make([]contract.ProjectItem, 0, len(view)),
		Suppressions: list(snap.Suppressions(proj)), Hint: hint}
	for _, a := range view {
		status, replacedBy := lifecycle(a.Status, a.ReplacedBy)
		item := contract.ProjectItem{Id: a.ID, Name: a.Name, Keywords: list(a.Keywords), Content: a.Content,
			Status: contract.ProjectItemStatusPublished, ReplacedBy: replacedBy, Scope: a.Scope, Enforced: a.Enforced}
		if status != nil {
			item.Status = contract.ProjectItemStatus(*status)
		}
		res.Items = append(res.Items, item)
	}
	// Encode once, as the response writer would (a trailing newline), so the
	// size checked is the size sent.
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(res); err != nil {
		return nil, err
	}
	if buf.Len() > maxViewBytes {
		return contract.GetProjectItems422JSONResponse(viewTooLarge), nil
	}
	return projectViewBody(buf.Bytes()), nil
}

// projectViewBody is the 200 response with the view already encoded, written
// as it is (pattern: specDocument).
type projectViewBody []byte

func (b projectViewBody) VisitGetProjectItemsResponse(w http.ResponseWriter) error {
	return writeFixed(w, "application/json", b, true)
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
	if r.Pattern == instructionsPattern && paramName(err) == "target" {
		writeJSON(w, http.StatusBadRequest, badTarget)
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
