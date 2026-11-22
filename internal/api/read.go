package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// maxQueryBytes bounds the search words, as the spec's maxLength on q says.
const maxQueryBytes = 1024

// badSearch is the 400 envelope for missing or invalid search words, whether
// the generated router or SearchArticles rejects them.
var badSearch = envelope("bad_request", "missing or invalid search words",
	"Pass one q parameter with 1 to 1024 bytes of search words.")

// projectIDPattern is the ProjectId schema's pattern, read from the spec so
// the contract is the one definition (ADR 0018).
var projectIDPattern = mustProjectPattern()

func mustProjectPattern() *regexp.Regexp {
	pattern, err := contract.ProjectIDPattern()
	if err != nil {
		panic(err)
	}
	return regexp.MustCompile(pattern)
}

// badProject is the 400 envelope for a project parameter that is not a ProjectId.
var badProject = envelope("bad_request", "invalid project",
	"Pass project as host/org/repo, for example github.com/acme/shop, or list the registered Projects with GET /api/v1/projects.")

// unregisteredHint tells a caller that named a Project this server does not
// hold that it got the global answer (ADR 0014).
const unregisteredHint = "The Project is not registered on this server, so this is the global answer. List the registered Projects with GET /api/v1/projects."

// maxProjectBytes is the spec's maxLength on ProjectId.
const maxProjectBytes = 512

// scopeHint validates the optional project parameter. It returns the Project
// to resolve for, the hint when it is not registered, and false when the
// parameter is not a ProjectId.
func (s *Server) scopeHint(project *contract.ProjectId) (proj string, hint *string, ok bool) {
	if project == nil {
		return "", nil, true
	}
	if len(*project) > maxProjectBytes || !projectIDPattern.MatchString(*project) {
		return "", nil, false
	}
	for _, p := range s.snap.Projects() {
		if strings.EqualFold(p, *project) {
			return *project, nil, true
		}
	}
	h := unregisteredHint
	return "", &h, true
}

// searchPattern is the pattern the generated router registers the search
// operation under; the mux reports it as the request's Pattern.
const searchPattern = http.MethodGet + " /api/v1/search"

// Server implements contract.StrictServerInterface over one Snapshot. The
// generated router decodes the parameters; these methods return the spec's
// response types. It carries no auth: the caller wraps /api/ in RequireToken.
type Server struct {
	snap     *engine.Snapshot
	specJSON []byte
}

var _ contract.StrictServerInterface = (*Server)(nil)

// NewServer returns the Server for snap.
func NewServer(snap *engine.Snapshot) (*Server, error) {
	specJSON, err := contract.SpecJSON()
	if err != nil {
		return nil, err
	}
	return &Server{snap: snap, specJSON: specJSON}, nil
}

// Register registers the spec's operations on m through the generated router,
// as "METHOD path" patterns. Every response carries
// X-Content-Type-Options: nosniff, parameters the router cannot decode are the
// 400 envelope, and a response that cannot be written is the 500 envelope.
func (s *Server) Register(m contract.ServeMux) {
	strict := contract.NewStrictHandlerWithOptions(s, []contract.StrictMiddlewareFunc{nosniff},
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
func (s *Server) SearchArticles(_ context.Context, req contract.SearchArticlesRequestObject) (contract.SearchArticlesResponseObject, error) {
	q := req.Params.Q
	if strings.TrimSpace(q) == "" || len(q) > maxQueryBytes {
		return contract.SearchArticles400JSONResponse(badSearch), nil
	}
	proj, hint, ok := s.scopeHint(req.Params.Project)
	if !ok {
		return contract.SearchArticles400JSONResponse(badProject), nil
	}
	res := contract.SearchArticles200JSONResponse{Query: q, Results: []contract.SearchHit{}, Hint: hint}
	for _, h := range s.snap.SearchIn(proj, q) {
		res.Results = append(res.Results, contract.SearchHit{
			Id:       h.ID,
			Name:     h.Name,
			Kind:     h.Kind,
			Scope:    h.Scope,
			Keywords: list(h.Keywords),
			Score:    h.Score,
			Matched:  list(h.Matched),
		})
	}
	return res, nil
}

// GetArticle answers GET /api/v1/articles/{id}. The router hands over the id
// decoded, so a nested id sent as one %2F-encoded segment keeps its "/"
// (ADR 0016).
func (s *Server) GetArticle(_ context.Context, req contract.GetArticleRequestObject) (contract.GetArticleResponseObject, error) {
	proj, hint, valid := s.scopeHint(req.Params.Project)
	if !valid {
		return contract.GetArticle400JSONResponse(badProject), nil
	}
	a, ok := s.snap.ArticleIn(proj, req.Id)
	if !ok {
		return contract.GetArticle404JSONResponse(envelope("not_found", "no such article",
			"Search for the article with the search operation, then use an id from its results.")), nil
	}
	return contract.GetArticle200JSONResponse{
		Id:       a.ID,
		Name:     a.Name,
		Keywords: list(a.Keywords),
		Content:  a.Content,
		Hint:     hint,
	}, nil
}

// ListProjects answers GET /api/v1/projects.
func (s *Server) ListProjects(context.Context, contract.ListProjectsRequestObject) (contract.ListProjectsResponseObject, error) {
	return contract.ListProjects200JSONResponse{Projects: list(s.snap.Projects())}, nil
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
