package engine_test

import (
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// #713 (slice of #34): a decision is an Item Kind. decisions/<name>.md loads
// at every Scope, the default read serves accepted decisions only, and
// supersession is checked at load. The seam is the engine API.

const (
	globalDecisionPath  = "global/decisions/"
	projectDecisionPath = "projects/" + govProject + "/decisions/"
	localDecisionPath   = ".goldie/decisions/"
)

// decisionDoc is a decision file: title, then the frontmatter lines in front.
func decisionDoc(title string, front ...string) string {
	return "---\ntitle: " + title + "\n" + strings.Join(front, "\n") + "\n---\nWe decided.\n"
}

func acceptedDecision(title string, extra ...string) string {
	return decisionDoc(title, append([]string{"status: accepted", "date: 2023-05-02"}, extra...)...)
}

func proposedDecision(title string) string {
	return decisionDoc(title, "status: proposed", "date: 2023-05-02")
}

func supersededDecision(by string) string {
	return decisionDoc("T", "status: superseded", "date: 2023-05-02", "superseded_by: "+by)
}

func decisionNames(ds []engine.Decision) string {
	names := []string{}
	for _, d := range ds {
		names = append(names, d.Name)
	}
	return strings.Join(names, ",")
}

func TestDecisionLoadsAtEveryScopeByItsFileStem(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalDecisionPath + "g-one.md":      acceptedDecision("Global One"),
		projectDecisionPath + "p-one.md":     acceptedDecision("Project One"),
		localDecisionPath + "l-one.md":       acceptedDecision("Local One"),
		globalDecisionPath + "keyworded.md":  acceptedDecision("Keyworded", "keywords: [db, queue]", "supersedes: [decision:g-one]"),
		projectDecisionPath + "unrelated.md": acceptedDecision("Unrelated"),
	})
	for _, tc := range []struct{ name, scope, title string }{
		{"g-one", engine.ScopeGlobal, "Global One"},
		{"p-one", govProject, "Project One"},
		{"l-one", engine.ScopeLocal, "Local One"},
	} {
		d, ok := snap.DecisionIn(govProject, tc.name)
		if !ok || d.Scope != tc.scope || d.Title != tc.title || d.Name != tc.name || d.Status != "accepted" {
			t.Errorf("DecisionIn(%s) = %+v, %v; want scope %s, title %q, accepted", tc.name, d, ok, tc.scope, tc.title)
		}
	}
	if _, ok := snap.DecisionIn("", "p-one"); ok {
		t.Error("a Project decision is served in the global view")
	}
	if got := decisionNames(snap.Decisions(govProject)); got != "g-one,keyworded,l-one,p-one,unrelated" {
		t.Errorf("Decisions(project) = %s", got)
	}
}

func TestDecisionIdentityIsTheFileStemNotTheTitle(t *testing.T) {
	snap := load(t, map[string]string{
		globalDecisionPath + "use-postgres.md": acceptedDecision("Something else entirely"),
	})
	d, ok := snap.DecisionIn("", "use-postgres")
	if !ok || d.Name != "use-postgres" || d.Title != "Something else entirely" {
		t.Fatalf("DecisionIn(use-postgres) = %+v, %v", d, ok)
	}
	if d, ok := snap.DecisionIn("", "Something else entirely"); ok {
		t.Errorf("a decision is found by its title: %+v", d)
	}
}

func TestDecisionNamesDifferingOnlyInCaseFailTheLoad(t *testing.T) {
	loadErr(t, map[string]string{
		globalDecisionPath + "Dup.md": acceptedDecision("a"),
		globalDecisionPath + "dup.md": acceptedDecision("b"),
	}, "Dup.md", "dup.md")
}

func TestDecisionSubdirectoryFailsTheLoad(t *testing.T) {
	loadErr(t, map[string]string{
		globalDecisionPath + "sub/x.md": acceptedDecision("x"),
	}, "global/decisions/sub")
}

func TestDecisionDefaultReadServesAcceptedOnly(t *testing.T) {
	snap := load(t, map[string]string{
		globalDecisionPath + "yes.md":   acceptedDecision("Yes"),
		globalDecisionPath + "maybe.md": proposedDecision("Maybe"),
		globalDecisionPath + "old.md":   supersededDecision("decision:yes"),
	})
	if got := decisionNames(snap.Decisions("")); got != "yes" {
		t.Errorf("Decisions default = %s, want yes", got)
	}
	if got := decisionNames(snap.Decisions("", engine.ReadOptions{})); got != "yes" {
		t.Errorf("Decisions(ReadOptions{}) = %s, want yes", got)
	}
	for _, name := range []string{"maybe", "old"} {
		if d, ok := snap.DecisionIn("", name); ok {
			t.Errorf("DecisionIn(%s) in the default read = %+v, want none", name, d)
		}
	}
	if got := decisionNames(snap.Decisions("", engine.ReadOptions{Drafts: true})); got != "maybe,old,yes" {
		t.Errorf("Decisions(Drafts) = %s, want maybe,old,yes", got)
	}
	if d, ok := snap.DecisionIn("", "maybe", engine.ReadOptions{Drafts: true}); !ok || d.Status != "proposed" {
		t.Errorf("DecisionIn(maybe, Drafts) = %+v, %v", d, ok)
	}
}

func TestDecisionStatusIsRequiredAndKnown(t *testing.T) {
	cases := map[string]string{
		"missing":   decisionDoc("T", "date: 2023-05-02"),
		"unknown":   decisionDoc("T", "status: SECRETVALUE", "date: 2023-05-02"),
		"empty":     decisionDoc("T", "status:", "date: 2023-05-02"),
		"list":      decisionDoc("T", "status: [accepted]", "date: 2023-05-02"),
		"published": decisionDoc("T", "status: published", "date: 2023-05-02"),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{globalDecisionPath + "x.md": doc}, "global/decisions/x.md", "status")
			if strings.Contains(msg, "SECRETVALUE") {
				t.Errorf("error echoes the status value: %q", msg)
			}
		})
	}
}

func TestDecisionSupersessionIsChecked(t *testing.T) {
	t.Run("a chain of three loads", func(t *testing.T) {
		snap := load(t, map[string]string{
			globalDecisionPath + "a.md": supersededDecision("decision:b"),
			globalDecisionPath + "b.md": supersededDecision("decision:c"),
			globalDecisionPath + "c.md": acceptedDecision("C"),
		})
		if d, ok := snap.DecisionIn("", "a", engine.ReadOptions{Drafts: true}); !ok || d.Status != "superseded" {
			t.Errorf("DecisionIn(a) = %+v, %v", d, ok)
		}
		if got := decisionNames(snap.Decisions("")); got != "c" {
			t.Errorf("Decisions = %s, want c", got)
		}
	})
	t.Run("superseded needs superseded_by", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalDecisionPath + "a.md": decisionDoc("T", "status: superseded", "date: 2023-05-02"),
		}, "global/decisions/a.md", "superseded_by")
	})
	t.Run("superseded_by needs status superseded", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalDecisionPath + "a.md": acceptedDecision("A", "superseded_by: decision:b"),
			globalDecisionPath + "b.md": acceptedDecision("B"),
		}, "global/decisions/a.md", "superseded_by")
	})
	t.Run("dangling", func(t *testing.T) {
		loadErr(t, map[string]string{globalDecisionPath + "a.md": supersededDecision("decision:nope")}, "global/decisions/a.md")
	})
	t.Run("self", func(t *testing.T) {
		loadErr(t, map[string]string{globalDecisionPath + "a.md": supersededDecision("decision:a")}, "global/decisions/a.md")
	})
	t.Run("cycle", func(t *testing.T) {
		msg := loadErr(t, map[string]string{
			globalDecisionPath + "a.md": supersededDecision("decision:b"),
			globalDecisionPath + "b.md": supersededDecision("decision:a"),
		})
		if !strings.Contains(msg, "global/decisions/a.md") && !strings.Contains(msg, "global/decisions/b.md") {
			t.Errorf("error %q names neither file", msg)
		}
	})
	t.Run("a global decision cannot name a Project one", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalDecisionPath + "a.md":  supersededDecision("decision:p"),
			projectDecisionPath + "p.md": acceptedDecision("P"),
		}, "global/decisions/a.md")
	})
	t.Run("a Project decision may name a global one", func(t *testing.T) {
		load(t, map[string]string{
			projectDecisionPath + "a.md": supersededDecision("decision:g"),
			globalDecisionPath + "g.md":  acceptedDecision("G"),
		})
	})
}

func TestDecisionResolvesByScope(t *testing.T) {
	files := map[string]string{
		globalDecisionPath + "x.md":  acceptedDecision("Global"),
		projectDecisionPath + "x.md": acceptedDecision("Project"),
		localDecisionPath + "x.md":   acceptedDecision("Local"),
	}
	snap := loadLocal(t, files)
	if d, ok := snap.DecisionIn(govProject, "x"); !ok || d.Scope != engine.ScopeLocal || d.Title != "Local" {
		t.Errorf("with all three, DecisionIn = %+v, %v; want local", d, ok)
	}
	delete(files, localDecisionPath+"x.md")
	snap = loadLocal(t, files)
	if d, ok := snap.DecisionIn(govProject, "x"); !ok || d.Scope != govProject {
		t.Errorf("project over global: DecisionIn = %+v, %v", d, ok)
	}
	if d, ok := snap.DecisionIn("", "x"); !ok || d.Scope != engine.ScopeGlobal {
		t.Errorf("global view: DecisionIn = %+v, %v", d, ok)
	}
}

func TestProposedProjectDecisionDoesNotHideAnAcceptedGlobalOne(t *testing.T) {
	snap := load(t, map[string]string{
		globalDecisionPath + "x.md":  acceptedDecision("Global"),
		projectDecisionPath + "x.md": proposedDecision("Project"),
	})
	d, ok := snap.DecisionIn(govProject, "x")
	if !ok || d.Scope != engine.ScopeGlobal || d.Status != "accepted" {
		t.Errorf("DecisionIn = %+v, %v; want the accepted global decision", d, ok)
	}
	if got := decisionNames(snap.Decisions(govProject)); got != "x" {
		t.Errorf("Decisions = %s, want x", got)
	}
}

func TestEnforcedGlobalDecisionCannotBeShadowed(t *testing.T) {
	t.Run("project over global", func(t *testing.T) {
		loadErr(t, map[string]string{
			globalDecisionPath + "x.md":  acceptedDecision("G", "enforced: true"),
			projectDecisionPath + "x.md": acceptedDecision("P"),
		}, "global/decisions/x.md", "projects/"+govProject+"/decisions/x.md")
	})
	t.Run("local over global", func(t *testing.T) {
		loadLocalErr(t, map[string]string{
			globalDecisionPath + "x.md": acceptedDecision("G", "enforced: true"),
			localDecisionPath + "x.md":  acceptedDecision("L"),
		}, "decision:x", localDecisionPath+"x.md")
	})
	t.Run("a proposed copy is no shadow", func(t *testing.T) {
		snap := load(t, map[string]string{
			globalDecisionPath + "x.md":  acceptedDecision("G", "enforced: true"),
			projectDecisionPath + "x.md": proposedDecision("P"),
		})
		if d, ok := snap.DecisionIn(govProject, "x"); !ok || d.Scope != engine.ScopeGlobal {
			t.Errorf("DecisionIn = %+v, %v; want the enforced global", d, ok)
		}
	})
}

func TestTeamSourceWithoutGlobalClaimCannotPublishAGlobalDecision(t *testing.T) {
	files := map[string]string{
		globalDecisionPath + "x.md":  acceptedDecision("G"),
		projectDecisionPath + "y.md": acceptedDecision("P"),
	}
	claimLoadErr(t, engine.Claim{Projects: []string{claimAcmeGlob}}, files, "global/decisions/x.md")
	snap := claimLoad(t, engine.Claim{Global: true, Projects: []string{claimAcmeGlob}}, files)
	if _, ok := snap.DecisionIn(govProject, "x"); !ok {
		t.Error("a claimed global decision is not served")
	}
}

func TestDecisionIsAKnownKindInReferences(t *testing.T) {
	old := "---\nname: Old\nstatus: deprecated\nreplaced_by: decision:new\n---\nBody\n"
	snap := load(t, map[string]string{
		globalDecisionPath + "new.md": acceptedDecision("New"),
		"global/articles/old.md":      old,
	})
	if a, ok := snap.ArticleIn("", "old", engine.ReadOptions{Drafts: true}); !ok || a.ReplacedBy != "decision:new" {
		t.Errorf("article old = %+v, %v; want replaced_by decision:new", a, ok)
	}
	// The reference is checked as a known Kind: a missing target fails the
	// load with the file named, not "unknown kind".
	loadErr(t, map[string]string{"global/articles/old.md": old}, "global/articles/old.md")
}

func TestDecisionTitleWithAHiddenRuneFailsTheLoad(t *testing.T) {
	for _, r := range []string{"\u202e", "\u200b", "\u2066"} {
		doc := "---\ntitle: \"Use" + r + "Postgres\"\nstatus: accepted\ndate: 2023-05-02\n---\nBody\n"
		msg := loadErr(t, map[string]string{globalDecisionPath + "x.md": doc}, "global/decisions/x.md")
		if strings.Contains(msg, r) {
			t.Errorf("error echoes the hidden rune %U", []rune(r)[0])
		}
	}
}

// #720 (slice of #34): decision files without goldie frontmatter load as
// Nygard (adr-tools) or MADR 2.x; frontmatter without a title falls back to
// the first H1 (MADR 3.x). The seam is the engine API and the load error.

// nygardDecision is an adr-tools file: H1, Date line, then the Status section.
func nygardDecision(h1, status string) string {
	return "# " + h1 + "\n\nDate: 2016-02-12\n\n## Status\n\n" + status +
		"\n\n## Context\n\nWe need to record decisions.\n\n## Decision\n\nWe will.\n"
}

// madr2Decision is a MADR 2.x file: H1, then the bullets in the order given.
func madr2Decision(h1 string, bullets ...string) string {
	return "# " + h1 + "\n\n" + strings.Join(bullets, "\n") +
		"\n\nTechnical Story: none\n\n## Context and Problem Statement\n\nWhy?\n"
}

func draftDecision(t *testing.T, files map[string]string, name string) engine.Decision {
	t.Helper()
	d, ok := load(t, files).DecisionIn("", name, engine.ReadOptions{Drafts: true})
	if !ok {
		t.Fatalf("DecisionIn(%s) found nothing", name)
	}
	return d
}

func TestNygardDecisionWithoutFrontmatterLoads(t *testing.T) {
	d := draftDecision(t, map[string]string{
		globalDecisionPath + "0001-record-architecture-decisions.md": nygardDecision("1. Record architecture decisions", "Accepted"),
	}, "0001-record-architecture-decisions")
	if d.Title != "Record architecture decisions" || d.Status != "accepted" || d.Date != "2016-02-12" ||
		d.Name != "0001-record-architecture-decisions" || d.SupersededBy != "" {
		t.Errorf("decision = %+v; want title without number, accepted, 2016-02-12, name = file stem", d)
	}
}

func TestNygardStatusIsTheFirstWordLowercased(t *testing.T) {
	for status, want := range map[string]string{
		"Proposed":                  "proposed",
		"REJECTED":                  "rejected",
		"Deprecated in favour of X": "deprecated",
	} {
		d := draftDecision(t, map[string]string{
			globalDecisionPath + "x.md": nygardDecision("1. X", status),
		}, "x")
		if d.Status != want {
			t.Errorf("status %q loads as %q, want %q", status, d.Status, want)
		}
	}
}

func TestNygardSupersedesAndAmendedByLinesAreIgnored(t *testing.T) {
	// The targets name no decision: ignoring the lines means no check on them.
	status := "Accepted\n\nSupersedes [1. Old](0001-old.md)\n\nAmended by [3. Later](0003-later.md)"
	d := draftDecision(t, map[string]string{
		globalDecisionPath + "x.md": nygardDecision("2. X", status),
	}, "x")
	if d.Status != "accepted" || d.SupersededBy != "" || len(d.Supersedes) != 0 {
		t.Errorf("decision = %+v; want accepted, no supersession", d)
	}
}

func TestNygardDateMustComeBeforeTheFirstH2(t *testing.T) {
	doc := "# 1. X\n\n## Status\n\nAccepted\n\n## Context\n\nDate: 2016-02-12\n"
	loadErr(t, map[string]string{globalDecisionPath + "x.md": doc}, "global/decisions/x.md", "date")
}

func TestMADR2DecisionWithoutFrontmatterLoads(t *testing.T) {
	d := draftDecision(t, map[string]string{
		globalDecisionPath + "0005-use-plain-junit5.md": madr2Decision("Use Plain JUnit5",
			"* Status: accepted", "* Deciders: alice, bob", "* Date: 2018-11-19"),
	}, "0005-use-plain-junit5")
	if d.Title != "Use Plain JUnit5" || d.Status != "accepted" || d.Date != "2018-11-19" || d.Name != "0005-use-plain-junit5" {
		t.Errorf("decision = %+v; want Use Plain JUnit5, accepted, 2018-11-19", d)
	}
}

func TestMADR2BulletsMayComeInAnyOrder(t *testing.T) {
	d := draftDecision(t, map[string]string{
		globalDecisionPath + "x.md": madr2Decision("X",
			"* Date: 2020-01-02", "* Deciders: carol", "* Status: proposed"),
	}, "x")
	if d.Status != "proposed" || d.Date != "2020-01-02" {
		t.Errorf("decision = %+v; want proposed, 2020-01-02", d)
	}
}

func TestDecisionWithoutFrontmatterSupersededByABareStem(t *testing.T) {
	newer := nygardDecision("2. Use Y", "Accepted")
	for name, old := range map[string]string{
		"nygard": nygardDecision("1. Use X", "Superseded by [2. Use Y](use-y.md)"),
		"madr2": madr2Decision("Use X",
			"* Status: superseded by [ADR-0002](use-y.md)", "* Date: 2018-11-19"),
	} {
		t.Run(name, func(t *testing.T) {
			d := draftDecision(t, map[string]string{
				globalDecisionPath + "use-x.md": old,
				globalDecisionPath + "use-y.md": newer,
			}, "use-x")
			if d.Status != "superseded" || d.SupersededBy != "decision:use-y" {
				t.Errorf("decision = %+v; want superseded by decision:use-y", d)
			}
		})
	}
}

func TestDecisionWithoutFrontmatterSupersessionTargetIsChecked(t *testing.T) {
	docs := map[string]func(link string) string{
		"nygard": func(link string) string { return nygardDecision("1. X", "Superseded by [Y]("+link+")") },
		"madr2": func(link string) string {
			return madr2Decision("X", "* Status: superseded by [Y]("+link+")", "* Date: 2018-11-19")
		},
	}
	for format, doc := range docs {
		for name, link := range map[string]string{
			"dangling":  "nope.md",
			"directory": "sub/y.md",
			"parent":    "../y.md",
			"URL":       "https://example.com/y.md",
			"no .md":    "y",
		} {
			t.Run(format+" "+name, func(t *testing.T) {
				loadErr(t, map[string]string{
					globalDecisionPath + "x.md": doc(link),
					globalDecisionPath + "y.md": nygardDecision("2. Y", "Accepted"),
				}, "global/decisions/x.md")
			})
		}
	}
}

func TestDecisionFrontmatterWithoutTitleFallsBackToTheFirstH1(t *testing.T) {
	doc := "---\nstatus: accepted\ndate: 2023-05-02\n---\n# Use Postgres\n\n## Context\n\nWhy.\n"
	d := draftDecision(t, map[string]string{globalDecisionPath + "0007-db.md": doc}, "0007-db")
	if d.Title != "Use Postgres" || d.Status != "accepted" || d.Date != "2023-05-02" || d.Name != "0007-db" {
		t.Errorf("decision = %+v; want title Use Postgres, accepted, 2023-05-02", d)
	}
}

func TestDecisionFrontmatterIsNotMixedWithTheBody(t *testing.T) {
	t.Run("a title in the frontmatter beats the H1", func(t *testing.T) {
		d := draftDecision(t, map[string]string{
			globalDecisionPath + "x.md": "---\ntitle: From frontmatter\nstatus: accepted\ndate: 2023-05-02\n---\n# From H1\n",
		}, "x")
		if d.Title != "From frontmatter" {
			t.Errorf("title = %q, want From frontmatter", d.Title)
		}
	})
	t.Run("status is not taken from the body", func(t *testing.T) {
		doc := "---\ntitle: T\ndate: 2023-05-02\n---\n# T\n\n## Status\n\nAccepted\n"
		loadErr(t, map[string]string{globalDecisionPath + "x.md": doc}, "global/decisions/x.md", "status")
	})
	t.Run("no title and no H1", func(t *testing.T) {
		doc := "---\nstatus: accepted\ndate: 2023-05-02\n---\nJust text.\n"
		loadErr(t, map[string]string{globalDecisionPath + "x.md": doc}, "global/decisions/x.md", "title")
	})
}

func TestDecisionWithoutFrontmatterFailsTheLoadWhenIncomplete(t *testing.T) {
	cases := map[string]string{
		"nygard no H1":            "Date: 2016-02-12\n\n## Status\n\nAccepted\n",
		"nygard no status":        "# 1. X\n\nDate: 2016-02-12\n\n## Context\n\nText.\n",
		"nygard unknown status":   nygardDecision("1. X", "SECRETVALUE"),
		"nygard amended status":   nygardDecision("1. X", "Amended"),
		"nygard no date":          "# 1. X\n\n## Status\n\nAccepted\n",
		"nygard bad date":         "# 1. X\n\nDate: 12 Feb 2016\n\n## Status\n\nAccepted\n",
		"madr2 no H1":             "* Status: accepted\n* Date: 2018-11-19\n",
		"madr2 no status":         madr2Decision("X", "* Date: 2018-11-19"),
		"madr2 unknown status":    madr2Decision("X", "* Status: SECRETVALUE", "* Date: 2018-11-19"),
		"madr2 no date":           madr2Decision("X", "* Status: accepted"),
		"madr2 bad date":          madr2Decision("X", "* Status: accepted", "* Date: yesterday"),
		"hidden rune in the H1":   nygardDecision("1. Use\u202ePostgres", "Accepted"),
		"zero-width in madr2 H1":  madr2Decision("Use\u200bPostgres", "* Status: accepted", "* Date: 2018-11-19"),
		"superseded without link": nygardDecision("1. X", "Superseded"),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{globalDecisionPath + "x.md": doc}, "global/decisions/x.md")
			for _, leak := range []string{"SECRETVALUE", "\u202e", "\u200b", "yesterday"} {
				if strings.Contains(msg, leak) {
					t.Errorf("error echoes a value from the file: %q", msg)
				}
			}
		})
	}
}
