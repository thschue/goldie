package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

// Handler returns s as the generated ServerInterface that
// contract.HandlerWithOptions routes to. Every response carries
// X-Content-Type-Options: nosniff, and a response that cannot be written is
// the 500 envelope.
func (s *Server) Handler() contract.ServerInterface {
	return contract.NewStrictHandlerWithOptions(s, []contract.StrictMiddlewareFunc{nosniff},
		contract.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  RequestError,
			ResponseErrorHandlerFunc: responseError,
		})
}

// SearchArticles answers GET /api/v1/search.
func (s *Server) SearchArticles(_ context.Context, req contract.SearchArticlesRequestObject) (contract.SearchArticlesResponseObject, error) {
	q := req.Params.Q
	if strings.TrimSpace(q) == "" || len(q) > maxQueryBytes {
		return contract.SearchArticles400JSONResponse(badSearch), nil
	}
	res := contract.SearchArticles200JSONResponse{Query: q, Results: []contract.SearchHit{}}
	for _, h := range s.snap.Search(q) {
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
	a, ok := s.snap.Article(req.Id)
	if !ok {
		return contract.GetArticle404JSONResponse(envelope("not_found", "no such article",
			"Search for the article with the search operation, then use an id from its results.")), nil
	}
	return contract.GetArticle200JSONResponse{
		Id:       a.ID,
		Name:     a.Name,
		Keywords: list(a.Keywords),
		Content:  a.Content,
	}, nil
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

func (d specDocument) VisitGetOpenAPIYAMLResponse(w http.ResponseWriter) error { return d.write(w) }

func (d specDocument) VisitGetOpenAPIJSONResponse(w http.ResponseWriter) error { return d.write(w) }

func (d specDocument) write(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", d.contentType)
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}

// nosniff sets X-Content-Type-Options on every operation's response.
func nosniff(next contract.StrictHandlerFunc, _ string) contract.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		return next(ctx, w, r, req)
	}
}

// RequestError answers a request whose parameters the generated router cannot
// decode with the 400 envelope; it is the ErrorHandlerFunc for
// contract.HandlerWithOptions. A missing or repeated q is the envelope
// SearchArticles gives for blank search words. The error is not echoed: it
// quotes the request.
func RequestError(w http.ResponseWriter, _ *http.Request, err error) {
	if paramName(err) == "q" {
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
		tooMany  *contract.TooManyValuesForParamError
	)
	switch {
	case errors.As(err, &required):
		return required.ParamName
	case errors.As(err, &format):
		return format.ParamName
	case errors.As(err, &tooMany):
		return tooMany.ParamName
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
