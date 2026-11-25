package goldie_test

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Acceptance tests for #890: download goldie binaries from a release. The
// seam is the release configuration: .goreleaser.yaml, the release-please
// workflow, the PR snapshot job in ci.yml and release-please-config.json.

type goreleaserConfig struct {
	Before struct {
		Hooks []any `yaml:"hooks"`
	} `yaml:"before"`
	Builds []struct {
		Main    string   `yaml:"main"`
		Env     []string `yaml:"env"`
		Flags   []string `yaml:"flags"`
		Ldflags []string `yaml:"ldflags"`
		Goos    []string `yaml:"goos"`
		Goarch  []string `yaml:"goarch"`
	} `yaml:"builds"`
	Archives []map[string]any `yaml:"archives"`
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
	} `yaml:"checksum"`
}

func loadGoreleaser(t *testing.T) goreleaserConfig {
	t.Helper()
	for _, name := range []string{".goreleaser.yaml", ".goreleaser.yml"} {
		b, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		var c goreleaserConfig
		if err := yaml.Unmarshal(b, &c); err != nil {
			t.Fatalf("%s does not parse: %v", name, err)
		}
		if len(c.Builds) != 1 {
			t.Fatalf("%s: builds = %d, want one build of cmd/goldie", name, len(c.Builds))
		}
		return c
	}
	t.Fatal("no .goreleaser.yaml: the release builds no binaries")
	return goreleaserConfig{}
}

func hookCommands(hooks []any) []string {
	var out []string
	for _, h := range hooks {
		switch v := h.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			if s, ok := v["cmd"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func TestGoreleaserBuildsGoldieForThreeOSesAndTwoArchitectures(t *testing.T) {
	b := loadGoreleaser(t).Builds[0]
	if b.Main != "./cmd/goldie" && b.Main != "cmd/goldie" {
		t.Errorf("main = %q, want ./cmd/goldie", b.Main)
	}
	for _, want := range []string{"linux", "darwin", "windows"} {
		if !slices.Contains(b.Goos, want) {
			t.Errorf("goos %v lacks %s", b.Goos, want)
		}
	}
	for _, want := range []string{"amd64", "arm64"} {
		if !slices.Contains(b.Goarch, want) {
			t.Errorf("goarch %v lacks %s", b.Goarch, want)
		}
	}
	if len(b.Goos) != 3 || len(b.Goarch) != 2 {
		t.Errorf("goos %v goarch %v: want exactly the six pairs", b.Goos, b.Goarch)
	}
}

func TestGoreleaserBuildIsStaticAndTrimmed(t *testing.T) {
	b := loadGoreleaser(t).Builds[0]
	if !slices.Contains(b.Env, "CGO_ENABLED=0") {
		t.Errorf("env %v lacks CGO_ENABLED=0 (static build)", b.Env)
	}
	if !slices.Contains(b.Flags, "-trimpath") {
		t.Errorf("flags %v lack -trimpath", b.Flags)
	}
}

func TestGoreleaserBinaryReportsTheReleaseVersionCommitAndDate(t *testing.T) {
	ld := strings.Join(loadGoreleaser(t).Builds[0].Ldflags, " ")
	const pkg = "github.com/thschue/goldie/internal/platform."
	re := regexp.MustCompile(`-X[ =]` + regexp.QuoteMeta(pkg) + `Version=\{\{\s*\.(Version|Tag)\s*\}\}`)
	if !re.MatchString(ld) {
		t.Errorf("ldflags %q do not set platform.Version from the release tag ({{.Version}})", ld)
	}
	for _, v := range []string{"Commit", "BuildDate"} {
		if !strings.Contains(ld, pkg+v+"=") {
			t.Errorf("ldflags %q do not set platform.%s", ld, v)
		}
	}
}

func TestGoreleaserAttachesArchivesWithChecksums(t *testing.T) {
	c := loadGoreleaser(t)
	if len(c.Archives) == 0 {
		t.Fatal("no archives: the release has nothing to download")
	}
	if c.Checksum.NameTemplate == "" {
		t.Error("checksum.name_template is unset: the release attaches no checksums file")
	}
}

func TestGoreleaserBuildsTheUIBeforeTheBinaryAndFailsOnThePlaceholder(t *testing.T) {
	cmds := hookCommands(loadGoreleaser(t).Before.Hooks)
	ui := slices.IndexFunc(cmds, func(s string) bool { return strings.Contains(s, "./run ui") })
	if ui < 0 {
		t.Fatalf("before hooks %v never run ./run ui: the binary would embed the placeholder", cmds)
	}
	guard := slices.IndexFunc(cmds, func(s string) bool {
		return strings.Contains(s, "dist/app/index.html") || strings.Contains(s, "ui.Built")
	})
	if guard < 0 || guard < ui {
		t.Errorf("before hooks %v lack a check, after ./run ui, that internal/ui/dist/app/index.html exists", cmds)
	}
}

func TestReleaseWorkflowRunsGoreleaserOnlyWhenReleasePleaseCreatedARelease(t *testing.T) {
	b, err := os.ReadFile(".github/workflows/release-please.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			If          string         `yaml:"if"`
			Needs       any            `yaml:"needs"`
			Outputs     map[string]any `yaml:"outputs"`
			Permissions map[string]any `yaml:"permissions"`
			Steps       []ciStep       `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatal(err)
	}
	found := false
	for id, j := range wf.Jobs {
		runs := false
		for _, s := range j.Steps {
			if strings.Contains(s.Uses, "goreleaser") || strings.Contains(s.Run, "goreleaser release") {
				runs = true
				if strings.Contains(s.Uses, "@") && !regexp.MustCompile(`@[0-9a-f]{40}\b`).MatchString(s.Uses) {
					t.Errorf("job %s: %q is not pinned to a commit SHA", id, s.Uses)
				}
			}
		}
		if !runs {
			continue
		}
		found = true
		if !strings.Contains(j.If, "release_created") {
			t.Errorf("job %s: if %q does not gate on release_created", id, j.If)
		}
		if !slices.Contains(needsOf(ciJob{Needs: j.Needs}), "release-please") {
			t.Errorf("job %s: needs %v lacks release-please", id, j.Needs)
		}
		if j.Permissions["contents"] != "write" {
			t.Errorf("job %s: permissions %v lack contents: write to attach assets", id, j.Permissions)
		}
	}
	if !found {
		t.Fatal("release-please.yml runs no goreleaser job: a release PR merge would tag 0.1.0 with no artefacts")
	}
	if rp, ok := wf.Jobs["release-please"]; !ok || rp.Outputs["release_created"] == nil {
		t.Error("release-please job exposes no release_created output for the goreleaser job to gate on")
	}
}

func TestCIRunsAGoreleaserSnapshotOnPullRequests(t *testing.T) {
	b, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		On   map[string]any `yaml:"on"`
		Jobs map[string]struct {
			If              string `yaml:"if"`
			ContinueOnError any    `yaml:"continue-on-error"`
			Steps           []struct {
				Uses            string         `yaml:"uses"`
				Run             string         `yaml:"run"`
				With            map[string]any `yaml:"with"`
				ContinueOnError any            `yaml:"continue-on-error"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &wf); err != nil {
		t.Fatal(err)
	}
	if _, ok := wf.On["pull_request"]; !ok {
		t.Fatal("ci.yml does not run on pull_request")
	}
	for id, j := range wf.Jobs {
		for _, s := range j.Steps {
			args, _ := s.With["args"].(string)
			viaRun := strings.Contains(s.Run, "goreleaser") && strings.Contains(s.Run, "--snapshot")
			viaAction := strings.Contains(s.Uses, "goreleaser") && strings.Contains(args, "--snapshot")
			if !viaRun && !viaAction {
				continue
			}
			if strings.Contains(j.If, "push") && !strings.Contains(j.If, "pull_request") {
				t.Errorf("job %s: if %q skips pull requests", id, j.If)
			}
			if j.ContinueOnError == true || s.ContinueOnError == true {
				t.Errorf("job %s: the snapshot must be able to fail the PR", id)
			}
			return
		}
	}
	t.Fatal("no ci.yml job runs goreleaser with --snapshot")
}

func TestReleasePleaseConfigGroupsTheChangelogIntoSections(t *testing.T) {
	b, err := os.ReadFile("release-please-config.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Sections []struct {
			Type    string `json:"type"`
			Section string `json:"section"`
			Hidden  bool   `json:"hidden"`
		} `json:"changelog-sections"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	shown := map[string]string{}
	for _, s := range cfg.Sections {
		if !s.Hidden && s.Section != "" {
			shown[s.Type] = s.Section
		}
	}
	for _, typ := range []string{"feat", "fix", "perf"} {
		if shown[typ] == "" {
			t.Errorf("changelog-sections has no visible section for %q", typ)
		}
	}
}
