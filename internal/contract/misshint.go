package contract

import "errors"

// ArticleMissHint is the API's hint for an article id that matches nothing in
// any Scope. The CLI and the MCP tools reword it with their own search
// operation; RewordMiss keeps that rewrite to this exact hint, so a
// Suppression's not_found, whose hint names the entry to remove, is untouched.
const ArticleMissHint = "Search for the article, then use an id from its results."

// SkillMissHint, AgentMissHint, LoopMissHint and DecisionMissHint are the API's hints for a skill, agent, loop or decision
// name that answers nothing in the view; the CLI rewords them the same way.
const (
	SkillMissHint    = "List the skills, then use a name from the list."
	AgentMissHint    = "List the agents, then use a name from the list."
	LoopMissHint     = "List the loops, then use a name from the list."
	DecisionMissHint = "List the decisions (add status=all for the others), then use a name from the list."
)

// RewordMiss sets the hint of a not_found APIError in err to reworded when it
// is exactly plain, the server's plain-miss hint. Any other error, and any
// other not_found hint, passes through unchanged. It returns err.
func RewordMiss(err error, plain, reworded string) error {
	var ae *APIError
	if errors.As(err, &ae) && ae.Code == "not_found" && ae.Hint == plain {
		ae.Hint = reworded
	}
	return err
}
