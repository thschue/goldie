package engine_test

import (
	"strings"
	"testing"
	"unicode"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #271: a project article that shadows a published enforced global Item
// fails the load, and a lookup line (`goldie lookup <id>`, in a fenced block or
// an inline code span) that does not resolve in its article's own Scope view
// fails the load. Both are checked through DirSource.Load; the engine-internal
// function names are the implementer's choice, so the behaviour is pinned at
// Load.

const otherGovProject = "github.com/acme/other"

func otherProjectFile(id string) string {
	return "projects/" + otherGovProject + "/articles/" + id + ".md"
}

// docWith is an article whose body is body, with extra frontmatter lines.
func docWith(name, body string, front ...string) string {
	return "---\nname: " + name + "\n" + strings.Join(front, "\n") + "\n---\n" + body + "\n"
}

func mustLoad(t *testing.T, files map[string]string) {
	t.Helper()
	if _, err := engine.NewDirSource(writeFiles(t, files)).Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// --- Enforced Items cannot be shadowed ---

func TestProjectArticleShadowingPublishedEnforcedGlobalFailsTheLoad(t *testing.T) {
	for name, front := range map[string][]string{
		"published by default": {"enforced: true"},
		"explicit published":   {"enforced: true", "status: published"},
		"deprecated":           {"enforced: true", "status: deprecated"},
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{
				globalFile("rules"):  govArticle("Rules", front...),
				projectFile("rules"): govArticle("Shop rules"),
			}, globalFile("rules"), projectFile("rules"))
			if strings.Contains(msg, "Shop rules") || strings.Contains(msg, "Body of") {
				t.Errorf("error echoes article content: %q", msg)
			}
		})
	}
}

func TestShadowingIgnoresCaseOfTheIdentity(t *testing.T) {
	loadErr(t, map[string]string{
		globalFile("rules"):  govArticle("Rules", "enforced: true"),
		projectFile("RULES"): govArticle("Shop rules"),
	}, globalFile("rules"), projectFile("RULES"))
}

func TestShadowingAppliesToNestedIds(t *testing.T) {
	loadErr(t, map[string]string{
		globalFile("team/policy"):  govArticle("Policy", "enforced: true"),
		projectFile("team/policy"): govArticle("Shop policy"),
	}, globalFile("team/policy"), projectFile("team/policy"))
}

func TestShadowedEnforcedItemFailsTheLoadWhenAnotherProjectIsFine(t *testing.T) {
	loadErr(t, map[string]string{
		globalFile("rules"):       govArticle("Rules", "enforced: true"),
		otherProjectFile("other"): govArticle("Other"),
		projectFile("rules"):      govArticle("Shop rules"),
	}, globalFile("rules"), projectFile("rules"))
}

func TestShadowingAnEnforcedDraftLoads(t *testing.T) {
	mustLoad(t, map[string]string{
		globalFile("rules"):  govArticle("Rules", "enforced: true", "status: draft"),
		projectFile("rules"): govArticle("Shop rules"),
	})
}

func TestShadowingANonEnforcedGlobalItemLoads(t *testing.T) {
	for _, front := range [][]string{nil, {"enforced: false"}, {"status: deprecated"}} {
		files := map[string]string{
			globalFile("rules"):  govArticle("Rules", front...),
			projectFile("rules"): govArticle("Shop rules"),
		}
		snap := load(t, files)
		a, ok := snap.ArticleIn(govProject, "rules")
		if !ok || a.Name != "Shop rules" {
			t.Errorf("front %v: project view = %+v, %v; want the project's own copy", front, a, ok)
		}
	}
}

func TestEnforcedGlobalItemWithoutProjectCopyLoads(t *testing.T) {
	snap := load(t, map[string]string{
		globalFile("rules"):  govArticle("Rules", "enforced: true"),
		projectFile("other"): govArticle("Other"),
	})
	if a, ok := snap.ArticleIn(govProject, "rules"); !ok || a.Name != "Rules" {
		t.Errorf("project view = %+v, %v; want the enforced global Item", a, ok)
	}
}

func TestEnforcedProjectItemIsNotShadowingAnything(t *testing.T) {
	mustLoad(t, map[string]string{
		projectFile("rules"):  govArticle("Shop rules", "enforced: true"),
		otherProjectFile("x"): govArticle("X"),
		// Another Project's same-named article is not a shadow either.
		otherProjectFile("rules"): govArticle("Other rules"),
	})
}

func TestEnforcedGlobalAndEnforcedProjectCopyFailsToo(t *testing.T) {
	loadErr(t, map[string]string{
		globalFile("rules"):  govArticle("Rules", "enforced: true"),
		projectFile("rules"): govArticle("Shop rules", "enforced: true"),
	}, globalFile("rules"), projectFile("rules"))
}

// --- Lookup lines ---

func fence(lines ...string) string {
	return "Intro.\n\n```sh\n" + strings.Join(lines, "\n") + "\n```\n"
}

func TestLookupLineThatResolvesLoads(t *testing.T) {
	for name, body := range map[string]string{
		"fenced":           fence("goldie lookup target"),
		"tilde fence":      "~~~\ngoldie lookup target\n~~~\n",
		"inline":           "Read `goldie lookup target` first.",
		"case-insensitive": "Read `goldie lookup TARGET` first.",
		"several":          fence("goldie lookup target", "echo hi", "goldie lookup target"),
	} {
		t.Run(name, func(t *testing.T) {
			mustLoad(t, map[string]string{
				globalFile("target"): govArticle("Target"),
				globalFile("guide"):  docWith("Guide", body),
			})
		})
	}
}

func TestDanglingLookupLineFailsTheLoadNamingArticleAndId(t *testing.T) {
	for name, body := range map[string]string{
		"fenced":      fence("goldie lookup missing-one"),
		"tilde fence": "~~~\ngoldie lookup missing-one\n~~~\n",
		"inline":      "Read `goldie lookup missing-one` first.",
		"second line": fence("goldie lookup target", "echo hi", "goldie lookup missing-one"),
		"second span": "See `goldie lookup target` and `goldie lookup missing-one`.",
		"in a list":   "- step\n- run `goldie lookup missing-one`\n",
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{
				globalFile("target"): govArticle("Target"),
				globalFile("guide"):  docWith("Guide", body),
			}, globalFile("guide"), "missing-one")
			if strings.Contains(msg, "Body of") {
				t.Errorf("error echoes article content: %q", msg)
			}
		})
	}
}

func TestDanglingLookupLineInAProjectArticleNamesTheProjectFile(t *testing.T) {
	loadErr(t, map[string]string{
		projectFile("guide"): docWith("Guide", "`goldie lookup missing-one`"),
	}, projectFile("guide"), "missing-one")
}

func TestLookupIdInProseOutsideCodeIsNotALookupLine(t *testing.T) {
	mustLoad(t, map[string]string{
		globalFile("guide"): docWith("Guide",
			"Run goldie lookup missing-one when stuck.\n\nOr goldie lookup missing-two.\n\n> goldie lookup missing-three\n\n"+
				"[goldie lookup missing-four](https://example.com)\n"),
	})
}

func TestInlineSpanWithExtraWordsIsNotALookupLine(t *testing.T) {
	for _, body := range []string{
		"`goldie lookup missing-one and more`",
		"`run goldie lookup missing-one`",
		"`goldie lookup`",
		"`goldie lookupx missing-one`",
		"`goldie search missing-one`",
	} {
		mustLoad(t, map[string]string{globalFile("guide"): docWith("Guide", body)})
	}
}

func TestFencedLineWithExtraWordsIsNotALookupLine(t *testing.T) {
	mustLoad(t, map[string]string{
		globalFile("guide"): docWith("Guide", fence("goldie lookup missing-one and more", "echo goldie lookup missing-two")),
	})
}

func TestLookupLineWithProjectFlagIsSkipped(t *testing.T) {
	for _, body := range []string{
		"`goldie lookup missing-one --project github.com/acme/shop`",
		fence("goldie lookup missing-one --project github.com/acme/shop"),
	} {
		mustLoad(t, map[string]string{globalFile("guide"): docWith("Guide", body)})
	}
}

func TestSkippedProjectLineDoesNotHideADanglingLineBesideIt(t *testing.T) {
	loadErr(t, map[string]string{
		globalFile("guide"): docWith("Guide", fence(
			"goldie lookup missing-one --project github.com/acme/shop",
			"goldie lookup missing-two")),
	}, globalFile("guide"), "missing-two")
}

func TestLookupResolvesInTheArticlesOwnScopeView(t *testing.T) {
	files := map[string]string{
		globalFile("shared"):       govArticle("Shared"),
		projectFile("only-shop"):   govArticle("Only shop"),
		otherProjectFile("anchor"): govArticle("Anchor"),
	}
	cases := []struct {
		name, file, id string
		ok             bool
	}{
		{"project article points at a global id", projectFile("g1"), "shared", true},
		{"project article points at its own id", projectFile("g2"), "only-shop", true},
		{"global article points at a global id", globalFile("g3"), "shared", true},
		{"global article points at a project-only id", globalFile("g4"), "only-shop", false},
		{"project article points at another project's id", otherProjectFile("g5"), "only-shop", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := map[string]string{c.file: docWith("Guide", "`goldie lookup "+c.id+"`")}
			for k, v := range files {
				f[k] = v
			}
			if c.ok {
				mustLoad(t, f)
				return
			}
			loadErr(t, f, c.file, c.id)
		})
	}
}

func TestLookupFollowsAShadowingProjectCopy(t *testing.T) {
	// The id exists in global and in the project; both resolve.
	mustLoad(t, map[string]string{
		globalFile("shared"):  govArticle("Shared"),
		projectFile("shared"): govArticle("Shop shared"),
		projectFile("guide"):  docWith("Guide", "`goldie lookup shared`"),
	})
}

func TestLookupOfANestedIdResolves(t *testing.T) {
	mustLoad(t, map[string]string{
		globalFile("team/policy"): govArticle("Policy"),
		globalFile("guide"):       docWith("Guide", "`goldie lookup team/policy`"),
	})
	loadErr(t, map[string]string{
		globalFile("team/policy"): govArticle("Policy"),
		globalFile("guide"):       docWith("Guide", "`goldie lookup team/missing`"),
	}, globalFile("guide"), "team/missing")
}

// --- Hostile lookup lines ---

func noHiddenRunes(t *testing.T, msg string) {
	t.Helper()
	for _, r := range msg {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || (unicode.Is(unicode.Zs, r) && r != ' ') {
			t.Errorf("error carries the hidden rune %U: %q", r, msg)
			return
		}
	}
}

func TestLookupIdWithHiddenOrControlRunesFailsWithoutEchoingThem(t *testing.T) {
	for name, id := range map[string]string{
		"zero width space": "miss\u200bing",
		"bidi override":    "miss\u202eing",
		"escape":           "miss\x1b[31ming",
		"nul":              "miss\x00ing",
		"del":              "miss\x7fing",
		"no-break space":   "miss\u00a0ing",
	} {
		for form, body := range map[string]string{
			"fenced": fence("goldie lookup " + id),
			"inline": "`goldie lookup " + id + "`",
		} {
			t.Run(name+"/"+form, func(t *testing.T) {
				msg := loadErr(t, map[string]string{
					globalFile("target"): govArticle("Target"),
					globalFile("guide"):  docWith("Guide", body),
				}, globalFile("guide"))
				noHiddenRunes(t, msg)
			})
		}
	}
}

func TestOversizedLookupIdFailsWithABoundedMessage(t *testing.T) {
	id := strings.Repeat("a", 20000)
	for form, body := range map[string]string{
		"fenced": fence("goldie lookup " + id),
		"inline": "`goldie lookup " + id + "`",
	} {
		t.Run(form, func(t *testing.T) {
			msg := loadErr(t, map[string]string{
				globalFile("guide"): docWith("Guide", body),
			}, globalFile("guide"))
			if len(msg) > 1024 || strings.Contains(msg, id[:512]) {
				t.Errorf("error is %d bytes and echoes the oversized id", len(msg))
			}
		})
	}
}

func TestCodeSpanSpanningLinesDoesNotBreakTheLoad(t *testing.T) {
	// Whether a span broken across lines is a lookup line is not pinned; a
	// resolving id loads either way, and a non-lookup span never fails.
	mustLoad(t, map[string]string{
		globalFile("target"): govArticle("Target"),
		globalFile("guide"): docWith("Guide",
			"Read `goldie lookup\ntarget` or `goldie\nsearch\nmissing-one`.\n\n`unclosed goldie lookup missing-two\n"),
	})
}

func TestUnterminatedFenceStillChecksItsLookupLines(t *testing.T) {
	// CommonMark closes an unterminated fence at the end of the document.
	loadErr(t, map[string]string{
		globalFile("guide"): docWith("Guide", "```\ngoldie lookup missing-one\n"),
	}, globalFile("guide"), "missing-one")
}

func TestLookupLinesInFrontmatterAreNotChecked(t *testing.T) {
	mustLoad(t, map[string]string{
		globalFile("guide"): docWith("Guide", "Body.", "summary: \"`goldie lookup missing-one`\""),
	})
}
