package contract

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stub implements the generated strict server interface. The compile-time
// assertion fails the build if a spec change removes or renames an operation.
type stub struct{}

var _ StrictServerInterface = stub{}

func (stub) GetOpenAPIJSON(context.Context, GetOpenAPIJSONRequestObject) (GetOpenAPIJSONResponseObject, error) {
	return GetOpenAPIJSON200JSONResponse{"openapi": "3.1.0"}, nil
}

func (stub) GetOpenAPIYAML(context.Context, GetOpenAPIYAMLRequestObject) (GetOpenAPIYAMLResponseObject, error) {
	return GetOpenAPIYAMLdefaultJSONResponse{
		Body:       ErrorEnvelope{Error: Error{Code: "boom", Message: "boom", Details: ErrorDetails{Hint: "boom"}}},
		StatusCode: http.StatusTeapot,
	}, nil
}

func (stub) SearchArticles(_ context.Context, req SearchArticlesRequestObject) (SearchArticlesResponseObject, error) {
	return SearchArticles200JSONResponse{Query: req.Params.Q, Results: []SearchHit{}}, nil
}

func (stub) ListProjects(context.Context, ListProjectsRequestObject) (ListProjectsResponseObject, error) {
	return ListProjects200JSONResponse{Projects: []ProjectId{}}, nil
}

func (stub) GetProject(_ context.Context, req GetProjectRequestObject) (GetProjectResponseObject, error) {
	return GetProject200JSONResponse{Id: req.Id, Suppressions: []string{}}, nil
}

func (stub) GetSync(context.Context, GetSyncRequestObject) (GetSyncResponseObject, error) {
	return GetSync200JSONResponse{Warnings: []ContentWarning{}}, nil
}

func (stub) PostSync(context.Context, PostSyncRequestObject) (PostSyncResponseObject, error) {
	return PostSync202JSONResponse{Hint: "started"}, nil
}

func (stub) GetArticle(_ context.Context, req GetArticleRequestObject) (GetArticleResponseObject, error) {
	return GetArticle200JSONResponse{Id: req.Id, Name: "n", Keywords: []string{}, Content: "c"}, nil
}

func (stub) GetActivity(context.Context, GetActivityRequestObject) (GetActivityResponseObject, error) {
	return GetActivity200TexteventStreamResponse{Body: strings.NewReader(": keepalive\n\n")}, nil
}

func TestStrictHandlerRoutesTheSpecOperations(t *testing.T) {
	srv := httptest.NewServer(Handler(NewStrictHandler(stub{}, nil)))
	t.Cleanup(srv.Close)

	for path, want := range map[string]struct {
		status int
		body   string
	}{
		"/openapi.json":                      {http.StatusOK, `"openapi":"3.1.0"`},
		"/openapi.yaml":                      {http.StatusTeapot, `"code":"boom"`},
		"/api/v1/search?q=go":                {http.StatusOK, `"query":"go"`},
		"/api/v1/articles/platform%2Fdeploy": {http.StatusOK, `"id":"platform/deploy"`},
	} {
		resp, err := http.Get(srv.URL + path) //nolint:gosec // test server URL
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if resp.StatusCode != want.status || !strings.Contains(string(body), want.body) {
			t.Errorf("%s: got %d %q, want %d containing %q", path, resp.StatusCode, body, want.status, want.body)
		}
	}
}
