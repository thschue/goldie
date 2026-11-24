package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance tests for #739 (part of #34): .goldie/config.yaml maps a
// decisions folder, so a repository that keeps its ADRs in docs/adr serves
// them as decisions of the local Scope without moving them.
//
//   - `decisions: {path: docs/adr}` is accepted. A repository with only
//     .goldie/config.yaml and no .goldie/articles gets the local Scope when
//     decisions.path is set; a repository with both serves both.
//   - Only files that parse as decisions load: README.md and a template are
//     skipped. A malformed ADR does not stop the others (the named error is
//     pinned at the engine, local_decisions_folder_test.go).
//   - The path is untrusted: relative, inside the repository, read through
//     os.Root. Absolute paths, .., symlinks out and control characters are
//     refused with an error that names .goldie/config.yaml, no path, no value.
//
// The record does not say how a skipped README is worded, so the tests pin
// that it is not served and does not fail the command.

const (
	ldNygardBody   = "NYGARD-BODY"
	ldMADRBody     = "MADR-BODY"
	ldFrontBody    = "FRONT-BODY"
	ldReadmeBody   = "README-BODY"
	ldTemplateBody = "TEMPLATE-BODY"
	ldBadBody      = "BAD-BODY"

	ldNygard = "# 1. Record architecture decisions\n\nDate: 2023-02-04\n\n## Status\n\nAccepted\n\n## Context\n\nWe need to record decisions.\n\n## Decision\n\nWe keep " + ldNygardBody + ".\n"
	ldMADR   = "# Use PostgreSQL\n\n* Status: accepted\n* Date: 2024-03-05\n\n## Context and Problem Statement\n\nA store is needed.\n\n## Decision Outcome\n\n" + ldMADRBody + "\n"
	ldFront  = "---\ntitle: Use a queue\nstatus: accepted\ndate: 2024-06-01\n---\n" + ldFrontBody + "\n"
	ldReadme = "# Architecture decisions\n\nThis folder holds the ADRs. " + ldReadmeBody + "\n"
	// The MADR template, as adr tooling ships it.
	ldTemplate = "# [short title of solved problem and solution]\n\n* Status: {proposed | rejected | accepted | deprecated | superseded by [ADR-0005](0005-example.md)}\n* Date: {YYYY-MM-DD when the decision was last updated}\n\n## Context and Problem Statement\n\n" + ldTemplateBody + "\n"

	ldConfig = "decisions:\n  path: docs/adr\n"

	ldValidNames = "0001-record-architecture-decisions,0002-use-postgres,0003-use-queue"
)

func ldADRs() map[string]string {
	return map[string]string{
		"docs/adr/0001-record-architecture-decisions.md": ldNygard,
		"docs/adr/0002-use-postgres.md":                  ldMADR,
		"docs/adr/0003-use-queue.md":                     ldFront,
		"docs/adr/README.md":                             ldReadme,
		"docs/adr/0000-template.md":                      ldTemplate,
		".goldie/config.yaml":                            ldConfig,
	}
}

type ldDecision struct{ Name, Title, Scope, Status string }

// ldList runs decisions list --json in root and returns the decisions and the
// raw output.
func ldList(t *testing.T, root string) ([]ldDecision, string) {
	t.Helper()
	out := lsMustRun(t, root, nil, "decisions", "list", "--json")
	var got struct{ Decisions []ldDecision }
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decisions list --json is not JSON: %v\n%q", err, out)
	}
	return got.Decisions, out
}

func ldNames(ds []ldDecision) string {
	var n []string
	for _, d := range ds {
		n = append(n, d.Name)
	}
	return strings.Join(n, ",")
}

// A repository with only .goldie/config.yaml serves its ADRs as local
// decisions: no .goldie/articles, no address, no token. README.md and the
// template are not served.
func TestCLIDecisionsFolderOfAConfigOnlyRepositoryIsServedAsLocalDecisions(t *testing.T) {
	root := lsRepo(t, ldADRs())
	got, all := ldList(t, root)
	if ldNames(got) != ldValidNames {
		t.Fatalf("decisions list = %s, want %s (README and the template skipped)", ldNames(got), ldValidNames)
	}
	for _, d := range got {
		if d.Scope != "local" || d.Status != "accepted" {
			t.Errorf("decision %+v, want scope local and status accepted", d)
		}
	}
	for name, want := range map[string]string{
		"0001-record-architecture-decisions": ldNygardBody,
		"0002-use-postgres":                  ldMADRBody,
		"0003-use-queue":                     ldFrontBody,
	} {
		out := lsMustRun(t, root, nil, "decisions", "show", name)
		if !strings.Contains(out, want) {
			t.Errorf("decisions show %s lacks %q:\n%s", name, want, out)
		}
	}
	for _, name := range []string{"README", "0000-template"} {
		if out, err := lsRun(t, root, nil, "decisions", "show", name); err == nil {
			t.Errorf("decisions show %s served a skipped file:\n%s", name, out)
		}
	}
	for _, bad := range []string{ldReadmeBody, ldTemplateBody} {
		if strings.Contains(all, bad) {
			t.Errorf("decisions list carries %q of a skipped file", bad)
		}
	}
}

// A config.yaml without decisions.path is still what goldie init writes: not
// a local Scope, so the client needs a server.
func TestCLIConfigWithoutDecisionsPathIsNotALocalScope(t *testing.T) {
	root := lsRepo(t, map[string]string{
		".goldie/config.yaml":           "project: github.com/acme/shop\n",
		"docs/adr/0002-use-postgres.md": ldMADR,
	})
	if out, err := lsRun(t, root, nil, "decisions", "list"); err == nil {
		t.Errorf("decisions list worked without a server or a decisions path:\n%s", out)
	}
}

// With articles and decisions, both are served by the one embedded server,
// and the example content's global decisions stay beside the local ones.
func TestCLIDecisionsFolderAndArticlesAreServedTogether(t *testing.T) {
	files := ldADRs()
	files[".goldie/articles/team/only-local.md"] = lsOnlyLocalItem
	files[".goldie/config.yaml"] = "project: github.com/acme/shop\ndir: content\ndecisions:\n  path: docs/adr\n"
	root := lsRepo(t, files)
	lsCopyContent(t, root, "content")

	if out := lsMustRun(t, root, nil, "lookup", "team/only-local"); !strings.Contains(out, lsLocalOnlyBody) {
		t.Errorf("lookup of the local article lacks its body:\n%s", out)
	}
	scopes := map[string]string{}
	got, _ := ldList(t, root)
	for _, d := range got {
		scopes[d.Name] = d.Scope
	}
	for _, name := range strings.Split(ldValidNames, ",") {
		if scopes[name] != "local" {
			t.Errorf("decision %s has scope %q, want local (all: %v)", name, scopes[name], scopes)
		}
	}
	if scopes["use-postgres"] != "global" {
		t.Errorf("the global decision use-postgres has scope %q, want global (all: %v)", scopes["use-postgres"], scopes)
	}
	for _, skipped := range []string{"README", "0000-template"} {
		if _, ok := scopes[skipped]; ok {
			t.Errorf("%s is served as a decision", skipped)
		}
	}
	if out := lsMustRun(t, root, nil, "search", "onlylocal"); !strings.Contains(out, "team/only-local") {
		t.Errorf("search lacks the local article:\n%s", out)
	}
}

// A malformed ADR does not stop the others from loading.
func TestCLIDecisionsFolderServesTheValidADRsBesideAMalformedOne(t *testing.T) {
	files := ldADRs()
	files["docs/adr/0004-broken.md"] = "---\ntitle: Broken\nstatus: bogus\ndate: 2024-06-01\n---\n" + ldBadBody + "\n"
	root := lsRepo(t, files)
	got, out := ldList(t, root)
	if ldNames(got) != ldValidNames {
		t.Errorf("decisions list = %s, want %s", ldNames(got), ldValidNames)
	}
	if strings.Contains(out, ldBadBody) {
		t.Errorf("the malformed ADR is served:\n%s", out)
	}
}

// decisions.path is untrusted. Every hostile value fails the command with no
// output and an error that names .goldie/config.yaml and leaks no path, no
// target and no part of the value, with or without .goldie/articles.
func TestCLIDecisionsFolderRefusesAHostilePath(t *testing.T) {
	outside := t.TempDir()
	lsWrite(t, outside, map[string]string{
		"0001-secret.md": "---\ntitle: Secret\nstatus: accepted\ndate: 2024-06-01\n---\n" + lsOutsideSecret + "\n",
	})
	rel := func(t *testing.T, root string) string {
		t.Helper()
		r, err := filepath.Rel(root, outside)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(r)
	}
	link := func(t *testing.T, at, target string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, at); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
	}
	for name, mk := range map[string]func(t *testing.T, root string) string{
		"absolute path":                func(t *testing.T, root string) string { return `"` + outside + `"` },
		"absolute path to the root":    func(t *testing.T, root string) string { return `"` + root + `"` },
		"parent directory":             func(t *testing.T, root string) string { return `".."` },
		"dot dot escape":               func(t *testing.T, root string) string { return `"` + rel(t, root) + `"` },
		"dot dot after a name":         func(t *testing.T, root string) string { return `"docs/../../` + rel(t, root) + `"` },
		"dot dot behind a dot segment": func(t *testing.T, root string) string { return `"./a/../` + rel(t, root) + `"` },
		"symlink to a directory outside": func(t *testing.T, root string) string {
			link(t, filepath.Join(root, "adr-link"), outside)
			return `adr-link`
		},
		"symlink in the middle of the path": func(t *testing.T, root string) string {
			link(t, filepath.Join(root, "real", "link"), outside)
			return `real/link`
		},
		"empty string":      func(t *testing.T, root string) string { return `""` },
		"integer":           func(t *testing.T, root string) string { return `5` },
		"list":              func(t *testing.T, root string) string { return `[a, b]` },
		"missing directory": func(t *testing.T, root string) string { return `no-such-directory` },
		"a file":            func(t *testing.T, root string) string { return `README.md` },
		"NUL byte":          func(t *testing.T, root string) string { return `"docs\x00adr"` },
		"newline":           func(t *testing.T, root string) string { return `"docs\nadr"` },
		"escape sequence":   func(t *testing.T, root string) string { return `"docs\e[31madr"` },
		"very long path":    func(t *testing.T, root string) string { return `"` + strings.Repeat("a/", 3000) + `"` },
	} {
		for _, withArticles := range []bool{false, true} {
			suffix := ", config only"
			if withArticles {
				suffix = ", with articles"
			}
			t.Run(name+suffix, func(t *testing.T) {
				files := map[string]string{"README.md": "readme\n", "docs/adr/0002-use-postgres.md": ldMADR}
				if withArticles {
					files[".goldie/articles/team/only-local.md"] = lsOnlyLocalItem
				}
				root := lsRepo(t, files)
				value := mk(t, root)
				lsWrite(t, root, map[string]string{".goldie/config.yaml": "decisions:\n  path: " + value + "\n"})
				for _, args := range [][]string{{"decisions", "list"}, {"decisions", "show", "0002-use-postgres"}, {"status"}} {
					out, err := lsRun(t, root, nil, args...)
					if err == nil {
						t.Fatalf("goldie %s succeeded with decisions.path %s:\n%s", strings.Join(args, " "), value, out)
					}
					if out != "" {
						t.Errorf("output on failure: %q", out)
					}
					msg := err.Error()
					if !strings.Contains(msg, ".goldie/config.yaml") {
						t.Errorf("error %q does not name .goldie/config.yaml", msg)
					}
					lsNoLeak(t, msg, append(lsRealPaths(t, root), outside)...)
					if strings.Contains(msg, lsOutsideSecret) {
						t.Errorf("error carries content from outside the repository: %q", msg)
					}
					if len(value) > 100 && strings.Contains(msg, strings.Trim(value, `"`)[:100]) {
						t.Errorf("error echoes the decisions.path value")
					}
				}
			})
		}
	}
}

// A symlink inside the decisions folder that leads out of the repository is
// never read: its target is not served.
func TestCLIDecisionsFolderDoesNotFollowAFileSymlinkOut(t *testing.T) {
	outside := t.TempDir()
	lsWrite(t, outside, map[string]string{
		"secret.md": "---\ntitle: Secret\nstatus: accepted\ndate: 2024-06-01\n---\n" + lsOutsideSecret + "\n",
	})
	root := lsRepo(t, ldADRs())
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "docs", "adr", "0009-secret.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	for _, args := range [][]string{{"decisions", "list", "--json"}, {"decisions", "show", "0009-secret"}} {
		out, err := lsRun(t, root, nil, args...)
		if strings.Contains(out, lsOutsideSecret) || (err != nil && strings.Contains(err.Error(), lsOutsideSecret)) {
			t.Errorf("goldie %s served the file the symlink points to:\n%s\n%v", strings.Join(args, " "), out, err)
		}
		if err != nil {
			lsNoLeak(t, err.Error(), append(lsRealPaths(t, root), outside)...)
		}
	}
}
