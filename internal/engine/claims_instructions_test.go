package engine_test

import (
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// An instruction clause is published content like an Item: the Claim covers it.
func TestClaimsCoverInstructionClauses(t *testing.T) {
	const globalFile = "global/instructions/use.md"
	const projFile = "projects/github.com/other/x/instructions/use.md"
	body := clause("use", 1, "claude", "text")
	claimLoadErr(t, engine.Claim{}, map[string]string{globalFile: body}, globalFile)
	claimLoadErr(t, engine.Claim{Global: true, Projects: []string{claimAcmeGlob}},
		map[string]string{projFile: body}, projFile)
	claimLoad(t, engine.Claim{Global: true, Projects: []string{"github.com/other/*"}},
		map[string]string{globalFile: body, projFile: body})
}
