package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A hostile URI is answered by the component, never by the SDK, which would
// quote the URI into the error data: a control character there makes invalid
// JSON and ends the session. Every one gets resource-not-found with a next
// step and none of the URI, and the session still answers.
func TestResourceReadOfAHostileURIIsNotFoundAndTheSessionLives(t *testing.T) {
	cs := toolSession(t, newSkillsAPI(t, "body", "deployer").srv.URL)
	for _, uri := range []string{
		"goldie://agents/a\x00b", "goldie://agents/a\x7fb", "goldie://skills/deployer/a\x00b",
		"goldie://skills/deployer/a\x7fb", "goldie://skills/de\x1bployer/SKILL.md", "goldie://skills/deployer/a\nb",
		"goldie://other/x\x00", "\x00", "goldie://skills/deployer/a‮b",
	} {
		_, err := cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri})
		var je *jsonrpc.Error
		if !errors.As(err, &je) || je.Code != sdk.CodeResourceNotFound {
			t.Errorf("read %q: want the resource-not-found error, got %v", uri, err)
			continue
		}
		if !strings.Contains(je.Message, "list_skills") || len(je.Data) != 0 || strings.ContainsAny(je.Message, "\x00\x7f") {
			t.Errorf("read %q: the error should name a next step and carry no data: %+v", uri, je)
		}
		if _, err := cs.ListTools(context.Background(), nil); err != nil {
			t.Fatalf("after reading %q the session does not answer: %v", uri, err)
		}
	}
}

// A miss on a prompt names a next step and echoes nothing.
func TestUnknownPromptNamesANextStep(t *testing.T) {
	cs := toolSession(t, newSkillsAPI(t, "body", "deployer").srv.URL)
	_, err := cs.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "goldie-nope"})
	if err == nil || !strings.Contains(err.Error(), "list_skills") || !strings.Contains(err.Error(), "fixed") || strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown prompt: %v", err)
	}
}

// A prompt's description names its skill and what it does.
func TestPromptDescriptionNamesTheSkill(t *testing.T) {
	cs := toolSession(t, newSkillsAPI(t, "body", "deployer").srv.URL)
	res, err := cs.ListPrompts(context.Background(), nil)
	if err != nil || len(res.Prompts) != 1 {
		t.Fatalf("prompts: %v %v", res, err)
	}
	if d := res.Prompts[0].Description; !strings.Contains(d, "deployer") || !strings.Contains(d, "instructions") {
		t.Errorf("description = %q", d)
	}
}
