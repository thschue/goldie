package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/contract"
)

const (
	// skillFileTemplate expands {+path} (reserved expansion), so a path with
	// slashes, such as scripts/run.sh, is one value. Simple {path} would escape
	// the slash, and a compliant client would send scripts%2Frun.sh.
	skillFileTemplate = "goldie://skills/{name}/{+path}"
	agentTemplate     = "goldie://agents/{name}"
)

// addResources registers the two goldie:// resource templates, and answers
// every resources/read itself. A read that fell through to the SDK would have
// the client's URI echoed in the error data, quoted so that a control
// character makes invalid JSON, so nothing falls through: a URI under
// goldie://skills/ is a skill file read, under goldie://agents/ an agent read,
// anything else is resource not found. Only goldie://instructions, a fixed URI
// with no client text in it, goes on to the SDK.
func addResources(s *sdk.Server, t *tools) {
	s.AddResourceTemplate(&sdk.ResourceTemplate{
		Name:        "skill-file",
		Title:       "A file of a skill's bundle",
		Description: "One file of a skill, by the skill's name and the file's path in its bundle, such as scripts/run.sh. The session's default Project picks the bundle.",
		URITemplate: skillFileTemplate,
		MIMEType:    "text/plain",
	}, t.readSkillFile)
	s.AddResourceTemplate(&sdk.ResourceTemplate{
		Name:        "agent",
		Title:       "An agent",
		Description: "An agent's name and description, by name.",
		URITemplate: agentTemplate,
		MIMEType:    "text/plain",
	}, t.readAgent)
	s.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			rr, ok := req.(*sdk.ReadResourceRequest)
			if !ok {
				return next(ctx, method, req)
			}
			uri := ""
			if rr.Params != nil {
				uri = rr.Params.URI
			}
			switch {
			case uri == instructionsURI:
				return next(ctx, method, req)
			case strings.HasPrefix(uri, "goldie://skills/"):
				return t.readSkillFile(ctx, rr)
			case strings.HasPrefix(uri, "goldie://agents/"):
				return t.readAgent(ctx, rr)
			}
			return nil, notFound()
		}
	})
}

// resourcePath splits uri, which must be goldie://<kind>/<name>[/<path>], into
// its parts. Nothing but the session selects a Project, and nothing may leave
// the bundle, so a query, a fragment, invalid UTF-8, or a segment that is not
// a clean path segment (skillNameOK: empty, dots only, an escape, a backslash,
// a hidden character) is refused, before any request.
func resourcePath(uri, kind string) (name, path string, ok bool) {
	rest, found := strings.CutPrefix(uri, "goldie://"+kind+"/")
	if !found || !utf8.ValidString(rest) || strings.ContainsAny(rest, "?#") {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	for _, p := range parts {
		if !skillNameOK(p) {
			return "", "", false
		}
	}
	if kind == "agents" && len(parts) != 1 || kind == "skills" && len(parts) < 2 {
		return "", "", false
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

// notFound is the protocol's resource-not-found error. It names a next step,
// and no URI, address, path or token: the client's own URI may carry anything.
func notFound() error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams,
		Message: "resource not found; list_skills and list_agents give valid names"}
}

func textResource(uri, body string) *sdk.ReadResourceResult {
	return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: uri, MIMEType: "text/plain", Text: body}}}
}

// resourceError words a failed read: a miss is resource-not-found.
func (t *tools) resourceError(err error) error {
	var e *contract.APIError
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		return notFound()
	}
	return wordError(err, t.local)
}

// readSkillFile answers goldie://skills/{name}/{+path}: a file of the bundle,
// cleaned and quoted line by line like get_skill does, and never served as a
// page.
func (t *tools) readSkillFile(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
	uri := req.Params.URI
	name, path, ok := resourcePath(uri, "skills")
	if !ok {
		return nil, notFound()
	}
	proj, err := t.sessionProject("")
	if err != nil {
		return nil, err
	}
	file, err := t.skillFile(ctx, name, path, proj)
	if err != nil {
		return nil, t.resourceError(err)
	}
	return textResource(uri, "File of a skill (each line starts with \"| \"; drop that prefix to read the file):\n\n"+
		quote(strings.TrimRight(contract.CleanText(string(file)), "\n"))), nil
}

// readAgent answers goldie://agents/{name}: the agent's name and description,
// which is all the API answers.
func (t *tools) readAgent(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
	uri := req.Params.URI
	name, _, ok := resourcePath(uri, "agents")
	if !ok {
		return nil, notFound()
	}
	proj, err := t.sessionProject("")
	if err != nil {
		return nil, err
	}
	raw, err := t.client.GetAgent(ctx, name, &contract.GetAgentParams{Project: proj})
	a, err := contract.Answer(raw, err, contract.ParseGetAgentResponse,
		func(r *contract.GetAgentResponse) *contract.Agent { return r.JSON200 })
	if err != nil {
		return nil, t.resourceError(err)
	}
	return textResource(uri, fmt.Sprintf("Agent\nname: %s\ndescription: %s\n",
		contract.CleanLine(a.Name), contract.CleanLine(a.Description))), nil
}
