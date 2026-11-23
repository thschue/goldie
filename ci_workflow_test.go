package goldie_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Acceptance tests for #503: the TypeScript drift check must block merges.
// web/src/api/schema.ts is generated code, excluded from review and from
// ./run size, so its only proof is ./run regen-ts-check. That proof counts
// only if the required "lint and test" check waits for it. These tests read
// .github/workflows/ci.yml and pin the structure that makes a drifted
// schema.ts fail the required check.

type ciStep struct {
	Name            string         `yaml:"name"`
	Uses            string         `yaml:"uses"`
	Run             string         `yaml:"run"`
	If              string         `yaml:"if"`
	ContinueOnError any            `yaml:"continue-on-error"`
	Env             map[string]any `yaml:"env"`
}

type ciJob struct {
	Name            string         `yaml:"name"`
	If              string         `yaml:"if"`
	Needs           any            `yaml:"needs"`
	ContinueOnError any            `yaml:"continue-on-error"`
	Permissions     map[string]any `yaml:"permissions"`
	Steps           []ciStep       `yaml:"steps"`
}

func loadCI(t *testing.T) map[string]ciJob {
	t.Helper()
	b, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]ciJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatalf("ci.yml does not parse: %v", err)
	}
	return wf.Jobs
}

func needsOf(j ciJob) []string {
	switch v := j.Needs.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func runsTSCheck(j ciJob) bool {
	for _, s := range j.Steps {
		if strings.Contains(s.Run, "./run regen-ts-check") {
			return true
		}
	}
	return false
}

// tsDriftJob is the one job that runs ./run regen-ts-check.
func tsDriftJob(t *testing.T) (string, ciJob) {
	t.Helper()
	var ids []string
	jobs := loadCI(t)
	for id, j := range jobs {
		if runsTSCheck(j) {
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 {
		t.Fatalf("jobs running ./run regen-ts-check = %v, want exactly one job of its own", ids)
	}
	return ids[0], jobs[ids[0]]
}

func TestCIRunsTheTypeScriptDriftCheckInAJobOfItsOwn(t *testing.T) {
	id, j := tsDriftJob(t)
	for _, other := range []string{"ux", "check", "vuln", "lint-and-test"} {
		if id == other {
			t.Fatalf("./run regen-ts-check runs in %q; it needs a job of its own, so a Playwright flake in ux cannot block merges", other)
		}
	}
	if runsTSCheck(loadCI(t)["ux"]) {
		t.Error("the ux job still runs ./run regen-ts-check; the check lives in its own job")
	}
	var node bool
	for _, s := range j.Steps {
		if strings.HasPrefix(s.Uses, "actions/setup-node@") {
			node = true
		}
		if strings.Contains(s.Run, "./run ux") || strings.Contains(s.Uses, "playwright") {
			t.Errorf("job %q is light: step %q must not run the browser tests", id, s.Name)
		}
	}
	if !node {
		t.Errorf("job %q has no setup-node step, so ./run regen-ts-check has no Node", id)
	}
	if len(j.Steps) == 0 || !strings.HasPrefix(j.Steps[0].Uses, "actions/checkout@") {
		t.Errorf("job %q does not start with a checkout", id)
	}
}

func TestCITypeScriptDriftJobCannotBeSkippedOrIgnored(t *testing.T) {
	id, j := tsDriftJob(t)
	// A skipped need is not "success", but a continue-on-error job reports
	// success, so neither may be set.
	if j.If != "" {
		t.Errorf("job %q has if: %q; a conditional job can be skipped", id, j.If)
	}
	if j.ContinueOnError != nil {
		t.Errorf("job %q sets continue-on-error: its failure would not block", id)
	}
	for _, s := range j.Steps {
		if s.ContinueOnError != nil {
			t.Errorf("job %q step %q sets continue-on-error", id, s.Name)
		}
		if s.If != "" && strings.Contains(s.Run, "regen-ts-check") {
			t.Errorf("the drift check step has if: %q; it must always run", s.If)
		}
		if strings.Contains(s.Run, "regen-ts-check") && strings.Contains(s.Run, "||") {
			t.Errorf("the drift check step swallows its status: %q", s.Run)
		}
	}
}

func TestCITypeScriptDriftJobPinsActionsAndKeepsPermissions(t *testing.T) {
	id, j := tsDriftJob(t)
	pinned := regexp.MustCompile(`@[0-9a-f]{40}( |$)`)
	for _, s := range j.Steps {
		if s.Uses != "" && !pinned.MatchString(s.Uses) {
			t.Errorf("job %q step uses %q, not pinned to a commit SHA", id, s.Uses)
		}
	}
	for k, v := range j.Permissions {
		if v != "read" && v != "none" {
			t.Errorf("job %q widens permission %s to %v", id, k, v)
		}
	}
}

func TestCIRequiredCheckWaitsForTheTypeScriptDriftJob(t *testing.T) {
	id, _ := tsDriftJob(t)
	lat, ok := loadCI(t)["lint-and-test"]
	if !ok {
		t.Fatal("no lint-and-test job")
	}
	if lat.Name != "lint and test" {
		t.Errorf("the required check's name = %q, want %q", lat.Name, "lint and test")
	}
	if lat.If != "always()" {
		t.Errorf("lint-and-test if = %q, want always() so a failed need cannot skip it into a pass", lat.If)
	}
	have := needsOf(lat)
	for _, want := range []string{"check", "vuln", id} {
		found := false
		for _, n := range have {
			found = found || n == want
		}
		if !found {
			t.Errorf("lint-and-test needs = %v, missing %q", have, want)
		}
	}
}

func TestCIRequiredCheckFailsUnlessTheTypeScriptDriftJobSucceeded(t *testing.T) {
	id, _ := tsDriftJob(t)
	lat := loadCI(t)["lint-and-test"]
	result := "${{ needs." + id + ".result }}"
	for _, s := range lat.Steps {
		for name, v := range s.Env {
			if v != result {
				continue
			}
			// The same shape as CHECK and VULN: the variable is compared to
			// success, and the comparison decides the step's status.
			cmp := `"$` + name + `" = success`
			if !strings.Contains(s.Run, cmp) {
				t.Errorf("the step reads %s but never requires %s", name, cmp)
			}
			for _, line := range strings.Split(s.Run, "\n") {
				if strings.Contains(line, cmp) && (strings.Contains(line, "|| true") || strings.HasPrefix(strings.TrimSpace(line), "echo")) {
					t.Errorf("the comparison does not decide the step's status: %q", line)
				}
			}
			for _, old := range []string{"CHECK", "VULN"} {
				if !strings.Contains(s.Run, `"$`+old+`" = success`) {
					t.Errorf("the step no longer requires %s = success", old)
				}
			}
			return
		}
	}
	t.Errorf("lint-and-test does not read %s through env like check and vuln", result)
}
