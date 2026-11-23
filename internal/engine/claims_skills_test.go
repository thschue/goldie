package engine_test

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// A skill bundle is published content like an Item: the Claim covers it.
func TestClaimsCoverSkillBundles(t *testing.T) {
	const globalSkill = "global/skills/deploy/SKILL.md"
	const projSkill = "projects/github.com/other/x/skills/deploy/SKILL.md"
	claimLoadErr(t, engine.Claim{}, map[string]string{globalSkill: "# D\n"}, "global/skills/deploy")
	claimLoadErr(t, engine.Claim{Projects: []string{claimAcmeGlob}}, map[string]string{globalSkill: "# D\n"}, "global/skills/deploy")
	msg := claimLoadErr(t, engine.Claim{Global: true, Projects: []string{claimAcmeGlob}},
		map[string]string{projSkill: "# D\n"}, "projects/github.com/other/x/skills/deploy")
	if strings.Contains(msg, "SKILL.md") {
		t.Errorf("error %q should name the bundle", msg)
	}
	claimLoad(t, engine.Claim{Global: true, Projects: []string{"github.com/other/*"}},
		map[string]string{globalSkill: "# D\n", projSkill: "# D\n"})
}
