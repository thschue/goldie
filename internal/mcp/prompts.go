package mcp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/contract"
)

// promptPrefix starts the name of every prompt: goldie-<skill name>.
const promptPrefix = "goldie-"

// promptDescription is the description of the prompt of one skill. It names
// the skill, whose name is clean, and nothing else of what a Source says.
func promptDescription(skill string) string {
	return "Loads the " + skill + " skill's instructions into this conversation."
}

// promptSnapshots hold, per session, the skill names (in the API's order) the
// session's prompts were made from. The prompt list is fixed when a session starts: the first
// prompts request of a session reads the skills once, and the session keeps
// that list. A new session reads them again.
type promptSnapshots struct {
	mu    sync.Mutex
	names map[*sdk.ServerSession][]string
}

// addPrompts makes the server answer prompts/list and prompts/get with one
// prompt per skill, per session. The SDK's own prompt table is one list for
// the whole server, so the two methods are answered here, in a receiving
// middleware, and the server advertises prompts without listChanged, for the
// reason New gives for tools.
func addPrompts(s *sdk.Server, t *tools) {
	snaps := &promptSnapshots{names: map[*sdk.ServerSession][]string{}}
	s.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			ss, _ := req.GetSession().(*sdk.ServerSession)
			if ss != nil {
				switch p := req.GetParams().(type) {
				case *sdk.ListPromptsParams:
					return t.listPrompts(ctx, s, snaps, ss)
				case *sdk.GetPromptParams:
					return t.getPrompt(ctx, s, snaps, ss, p.Name)
				}
			}
			return next(ctx, method, req)
		}
	})
}

// snapshot returns the session's skill names in the API's order, reading them
// on the session's first use. A failed read is not kept, so the next request
// tries again.
func (t *tools) snapshot(ctx context.Context, s *sdk.Server, snaps *promptSnapshots, ss *sdk.ServerSession, failed surface) ([]string, error) {
	snaps.mu.Lock()
	names, ok := snaps.names[ss]
	snaps.mu.Unlock()
	if ok {
		return names, nil
	}
	proj, err := t.sessionProjectFor("", failed)
	if err != nil {
		return nil, err
	}
	raw, err := t.client.ListSkills(ctx, &contract.ListSkillsParams{Project: proj})
	res, err := contract.Answer(raw, err, contract.ParseListSkillsResponse,
		func(r *contract.ListSkillsResponse) *contract.SkillList { return r.JSON200 })
	if err != nil {
		return nil, wordErrorFor(err, t.local, failed)
	}
	names = nil
	seen := map[string]bool{}
	for _, sk := range res.Skills {
		// A name from a Source is a prompt only when it is one clean path segment.
		if !skillNameOK(sk.Name) || contract.CleanLine(sk.Name) != sk.Name || seen[sk.Name] {
			continue
		}
		seen[sk.Name] = true
		names = append(names, sk.Name)
	}
	snaps.mu.Lock()
	defer snaps.mu.Unlock()
	if have, ok := snaps.names[ss]; ok {
		return have, nil // another request of the session was first
	}
	live := map[*sdk.ServerSession]bool{}
	for o := range s.Sessions() {
		live[o] = true
	}
	for old := range snaps.names {
		if !live[old] {
			delete(snaps.names, old)
		}
	}
	snaps.names[ss] = names
	return names, nil
}

func (t *tools) listPrompts(ctx context.Context, s *sdk.Server, snaps *promptSnapshots, ss *sdk.ServerSession) (sdk.Result, error) {
	names, err := t.snapshot(ctx, s, snaps, ss, surfacePrompts)
	if err != nil {
		return nil, err
	}
	res := &sdk.ListPromptsResult{Prompts: []*sdk.Prompt{}}
	for _, n := range names {
		res.Prompts = append(res.Prompts, &sdk.Prompt{Name: promptPrefix + n, Description: promptDescription(n)})
	}
	return res, nil
}

func (t *tools) getPrompt(ctx context.Context, s *sdk.Server, snaps *promptSnapshots, ss *sdk.ServerSession, name string) (sdk.Result, error) {
	names, err := t.snapshot(ctx, s, snaps, ss, surfacePrompt)
	if err != nil {
		return nil, err
	}
	// The name is the client's, and may carry anything: an unknown one is not echoed.
	unknown := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams,
		Message: "unknown prompt; the prompt list is fixed for this session, so call list_skills to see the skills, or restart the MCP session to pick up new ones"}
	skill, ok := strings.CutPrefix(name, promptPrefix)
	if !ok || !slices.Contains(names, skill) {
		return nil, unknown
	}
	proj, err := t.sessionProjectFor("", surfacePrompt)
	if err != nil {
		return nil, err
	}
	file, err := t.skillFile(ctx, skill, "SKILL.md", proj)
	if err != nil {
		var e *contract.APIError
		if errors.As(err, &e) && e.Status == http.StatusNotFound {
			return nil, unknown
		}
		return nil, wordErrorFor(err, t.local, surfacePrompt)
	}
	return &sdk.GetPromptResult{
		Description: promptDescription(skill),
		Messages: []*sdk.PromptMessage{{
			Role:    "user",
			Content: &sdk.TextContent{Text: contract.CleanText(string(file))},
		}},
	}, nil
}
