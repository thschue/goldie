package engine_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #414 (slice 2 of #18): an instruction clause is an Item of Kind
// instruction, the file instructions/<name>.md, in the three Scopes:
// global/instructions/, projects/<host>/<org>/<repo>/instructions/ and
// .goldie/instructions/. Clauses resolve per name through Snapshot.winner (ADR
// 0021), then sort by order, then by name. A built-in default clause applies
// only when no clause resolves for that Project and target. Templating is
// literal replacement of exactly {{project}}, {{mcp_name}} and {{cli}}; any
// other {{x}} fails the load, naming the file.
//
// The API the tests assume, following agents.go (the issue names the call but
// not its types):
//
//	type InstructionVars struct {
//		MCPName string // the value of {{mcp_name}}
//		CLI     string // the value of {{cli}}
//	}
//	func (s *Snapshot) Instructions(proj, target string, vars InstructionVars) (block, digest string)
//
// {{project}} is proj. The frontmatter has name (equal to the slug, ADR 0015),
// order (an integer) and targets (a list of "claude" and "agents"), plus the
// lifecycle keys every Kind has. The tests assert neither the separator
// between clauses, nor the text of the default clause, nor the digest's
// format, which the issue leaves open.

func globalClauseFile(name string) string { return "global/instructions/" + name + ".md" }

func projectClauseFile(name string) string {
	return "projects/" + govProject + "/instructions/" + name + ".md"
}

func localClauseFile(name string) string { return ".goldie/instructions/" + name + ".md" }

// clause is an instruction file. targets is the frontmatter flow list, such as
// "claude, agents"; extra is further frontmatter lines.
func clause(name string, order int, targets, body string, extra ...string) string {
	front := "name: " + name + "\norder: " + strconv.Itoa(order) + "\ntargets: [" + targets + "]\n"
	if len(extra) > 0 {
		front += strings.Join(extra, "\n") + "\n"
	}
	return "---\n" + front + "---\n" + body + "\n"
}

var testVars = engine.InstructionVars{MCPName: "goldie", CLI: "goldie"}

// inOrder reports whether each of marks appears in block, after the one before.
func inOrder(block string, marks ...string) bool {
	at := 0
	for _, m := range marks {
		i := strings.Index(block[at:], m)
		if i < 0 {
			return false
		}
		at += i + len(m)
	}
	return true
}

// defaultBlock is what the built-in default clause renders to for a Project
// and target: the block of a Snapshot with no clauses.
func defaultBlock(t *testing.T, proj, target string) string {
	t.Helper()
	block, _ := load(t, map[string]string{"global/articles/a.md": article("A")}).Instructions(proj, target, testVars)
	if strings.TrimSpace(block) == "" {
		t.Fatal("the built-in default clause renders to nothing")
	}
	return block
}

func digestOf(s *engine.Snapshot, proj, target string, v engine.InstructionVars) string {
	_, d := s.Instructions(proj, target, v)
	return d
}

// --- Criterion 1: clauses, resolution, order, targets ---

func TestClausesSortByOrderThenName(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("zeta"):  clause("zeta", 1, "claude", "MARK-ZETA"),
		globalClauseFile("beta"):  clause("beta", 1, "claude", "MARK-BETA"),
		globalClauseFile("alpha"): clause("alpha", 2, "claude", "MARK-ALPHA"),
		globalClauseFile("first"): clause("first", -5, "claude", "MARK-FIRST"),
	})
	block, _ := snap.Instructions("", "claude", testVars)
	if !inOrder(block, "MARK-FIRST", "MARK-BETA", "MARK-ZETA", "MARK-ALPHA") {
		t.Errorf("clauses are not by order then name: %q", block)
	}
}

func TestClausesApplyOnlyToTheirTargets(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("both"):       clause("both", 1, "claude, agents", "MARK-BOTH"),
		globalClauseFile("claude-doc"): clause("claude-doc", 2, "claude", "MARK-CLAUDE"),
		globalClauseFile("agents-doc"): clause("agents-doc", 3, "agents", "MARK-AGENTS"),
	})
	claude, _ := snap.Instructions("", "claude", testVars)
	agents, _ := snap.Instructions("", "agents", testVars)
	for _, w := range []string{"MARK-BOTH", "MARK-CLAUDE"} {
		if !strings.Contains(claude, w) {
			t.Errorf("claude block lacks %s: %q", w, claude)
		}
	}
	if strings.Contains(claude, "MARK-AGENTS") {
		t.Errorf("claude block carries an agents-only clause: %q", claude)
	}
	for _, w := range []string{"MARK-BOTH", "MARK-AGENTS"} {
		if !strings.Contains(agents, w) {
			t.Errorf("agents block lacks %s: %q", w, agents)
		}
	}
	if strings.Contains(agents, "MARK-CLAUDE") {
		t.Errorf("agents block carries a claude-only clause: %q", agents)
	}
}

func TestProjectClauseWinsOverTheGlobalOneOfTheSameName(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"):        clause("use", 1, "claude", "MARK-GLOBAL-USE"),
		globalClauseFile("keep"):       clause("keep", 2, "claude", "MARK-KEEP"),
		projectClauseFile("use"):       clause("use", 3, "claude", "MARK-SHOP-USE"),
		projectClauseFile("shop-only"): clause("shop-only", 4, "claude", "MARK-SHOP-ONLY"),
	})
	shop, _ := snap.Instructions(govProject, "claude", testVars)
	if strings.Contains(shop, "MARK-GLOBAL-USE") || !inOrder(shop, "MARK-KEEP", "MARK-SHOP-USE", "MARK-SHOP-ONLY") {
		t.Errorf("Project view = %q; want keep, the Project use (not the global one), shop-only", shop)
	}
	for _, proj := range []string{"", "github.com/acme/other"} {
		got, _ := snap.Instructions(proj, "claude", testVars)
		if !inOrder(got, "MARK-GLOBAL-USE", "MARK-KEEP") || strings.Contains(got, "MARK-SHOP") {
			t.Errorf("view %q = %q; want the global clauses only", proj, got)
		}
	}
}

func TestProjectClauseMatchesCaseInsensitively(t *testing.T) {
	snap := load(t, map[string]string{
		projectClauseFile("use"): clause("use", 1, "claude", "MARK-SHOP-USE"),
	})
	got, _ := snap.Instructions(strings.ToUpper(govProject), "claude", testVars)
	if !strings.Contains(got, "MARK-SHOP-USE") {
		t.Errorf("upper-case Project view = %q", got)
	}
}

func TestWinningClauseReplacesTheWholeClauseIncludingTargets(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"):  clause("use", 1, "claude", "MARK-GLOBAL-USE"),
		projectClauseFile("use"): clause("use", 1, "agents", "MARK-SHOP-USE"),
	})
	got, _ := snap.Instructions(govProject, "claude", testVars)
	if strings.Contains(got, "MARK-GLOBAL-USE") || strings.Contains(got, "MARK-SHOP-USE") {
		t.Errorf("claude block of the Project = %q; the Project clause wins and is not for claude, so neither shows", got)
	}
}

func TestLocalClauseWinsInEveryView(t *testing.T) {
	snap := loadLocal(t, map[string]string{
		globalClauseFile("use"):  clause("use", 1, "claude", "MARK-GLOBAL-USE"),
		projectClauseFile("use"): clause("use", 1, "claude", "MARK-SHOP-USE"),
		localClauseFile("use"):   clause("use", 1, "claude", "MARK-LOCAL-USE"),
	})
	for _, proj := range []string{"", govProject} {
		got, _ := snap.Instructions(proj, "claude", testVars)
		if !strings.Contains(got, "MARK-LOCAL-USE") || strings.Contains(got, "MARK-GLOBAL-USE") || strings.Contains(got, "MARK-SHOP-USE") {
			t.Errorf("view %q = %q; want only the local clause", proj, got)
		}
	}
}

func TestEnforcedGlobalClauseCannotBeShadowedByAProject(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"):  clause("use", 1, "claude", "MARK-GLOBAL-USE", "enforced: true"),
		projectClauseFile("use"): clause("use", 1, "claude", "MARK-SHOP-USE"),
	})
	got, _ := snap.Instructions(govProject, "claude", testVars)
	if !strings.Contains(got, "MARK-GLOBAL-USE") || strings.Contains(got, "MARK-SHOP-USE") {
		t.Errorf("Project view = %q; an enforced global clause answers", got)
	}
}

func TestClausesAreNotArticlesOrAgents(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude", "MARK"),
		"global/articles/a.md":  article("A"),
	})
	if got := len(snap.Articles()); got != 1 {
		t.Errorf("Articles() = %d, want only the article", got)
	}
	if got := snap.Agents(""); len(got) != 0 {
		t.Errorf("Agents() = %v, want none", got)
	}
}

// --- Criterion 2: the default clause ---

func TestDefaultClauseAppliesWhenNoClauseExists(t *testing.T) {
	for _, target := range []string{"claude", "agents"} {
		for _, proj := range []string{"", govProject} {
			defaultBlock(t, proj, target)
		}
	}
}

func TestDefaultClauseIsLeftOutOnceAClauseResolves(t *testing.T) {
	def := defaultBlock(t, "", "claude")
	snap := load(t, map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude", "MARK-USE"),
	})
	got, _ := snap.Instructions("", "claude", testVars)
	if !strings.Contains(got, "MARK-USE") {
		t.Fatalf("block lacks the clause: %q", got)
	}
	if strings.Contains(got, strings.TrimSpace(def)) {
		t.Errorf("block still carries the default clause: %q", got)
	}
}

func TestDefaultClauseAppliesPerTarget(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude", "MARK-USE"),
	})
	got, _ := snap.Instructions("", "agents", testVars)
	if strings.Contains(got, "MARK-USE") {
		t.Errorf("agents block carries a claude-only clause: %q", got)
	}
	if want := defaultBlock(t, "", "agents"); got != want {
		t.Errorf("agents block = %q, want the default %q: no clause resolves for that target", got, want)
	}
}

func TestDefaultClauseAppliesPerProject(t *testing.T) {
	snap := load(t, map[string]string{
		projectClauseFile("use"): clause("use", 1, "claude", "MARK-SHOP-USE"),
	})
	if got, _ := snap.Instructions(govProject, "claude", testVars); !strings.Contains(got, "MARK-SHOP-USE") {
		t.Errorf("Project view = %q", got)
	}
	got, _ := snap.Instructions("", "claude", testVars)
	if strings.Contains(got, "MARK-SHOP-USE") || got != defaultBlock(t, "", "claude") {
		t.Errorf("global view = %q; no clause resolves there, so only the default shows", got)
	}
}

func TestDraftClauseDoesNotResolveSoTheDefaultApplies(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude", "MARK-DRAFT", "status: draft"),
	})
	got, _ := snap.Instructions("", "claude", testVars)
	if strings.Contains(got, "MARK-DRAFT") || got != defaultBlock(t, "", "claude") {
		t.Errorf("block = %q; a draft clause is not served, so the default applies", got)
	}
}

// --- Criterion 3: literal templating ---

func TestTemplateReplacesTheThreeVariablesLiterally(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude",
			"P=[{{project}}] M=[{{mcp_name}}] C=[{{cli}}] again P=[{{project}}] C=[{{cli}}]"),
	})
	got, _ := snap.Instructions(govProject, "claude", engine.InstructionVars{MCPName: "acme-mcp", CLI: "acme-cli"})
	want := "P=[" + govProject + "] M=[acme-mcp] C=[acme-cli] again P=[" + govProject + "] C=[acme-cli]"
	if !strings.Contains(got, want) {
		t.Errorf("block = %q, want it to contain %q", got, want)
	}
	if strings.Contains(got, "{{") {
		t.Errorf("block still holds a placeholder: %q", got)
	}
}

func TestTemplateOfAnUnregisteredProjectUsesTheProjectGiven(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude", "for {{project}}"),
	})
	got, _ := snap.Instructions("github.com/acme/unregistered", "claude", testVars)
	if !strings.Contains(got, "for github.com/acme/unregistered") {
		t.Errorf("block = %q", got)
	}
}

func TestSubstitutedValuesAreNotExpandedAgain(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude", "M=[{{mcp_name}}]"),
	})
	got, _ := snap.Instructions("", "claude", engine.InstructionVars{MCPName: "{{cli}}", CLI: "ZZ-CLI-VALUE"})
	if strings.Contains(got, "ZZ-CLI-VALUE") {
		t.Errorf("a substituted value was expanded again: %q", got)
	}
}

func TestSubstitutedValuesAreCleanedOfHiddenCharacters(t *testing.T) {
	snap := load(t, map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude", "P=[{{project}}] M=[{{mcp_name}}] C=[{{cli}}]"),
	})
	hostile := "evil\n## Injected\r\u202e\u200b\x1b[31m\u0085\u2028x"
	got, _ := snap.Instructions("github.com/acme/"+hostile, "claude",
		engine.InstructionVars{MCPName: hostile, CLI: hostile})
	for _, r := range got {
		if r != '\n' && r != '\t' && engine.HiddenRune(r) {
			t.Errorf("block carries the hidden character %U: %q", r, got)
			break
		}
	}
	if strings.Contains(got, "\n## Injected") || strings.Contains(got, "\u2028") {
		t.Errorf("a value broke out of its line: %q", got)
	}
}

func TestUnknownPlaceholdersFailTheLoadNamingTheFile(t *testing.T) {
	for name, body := range map[string]string{
		"unknown":     "see {{unknown}}",
		"capitalised": "see {{Project}}",
		"spaced":      "see {{ project }}",
		"go-template": "see {{.Project}}",
		"function":    `see {{printf "%s" "x"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			msg := loadErr(t, map[string]string{
				globalClauseFile("ok"):  clause("ok", 1, "claude", "fine {{cli}}"),
				globalClauseFile("bad"): clause("bad", 2, "claude", body),
			}, globalClauseFile("bad"))
			if strings.Contains(msg, globalClauseFile("ok")) {
				t.Errorf("error names the good file too: %q", msg)
			}
		})
	}
}

func TestUnknownPlaceholderFailsTheLoadInEveryScope(t *testing.T) {
	loadErr(t, map[string]string{
		projectClauseFile("bad"): clause("bad", 1, "claude", "see {{nope}}"),
	}, projectClauseFile("bad"))
	loadLocalErr(t, map[string]string{
		localClauseFile("bad"): clause("bad", 1, "claude", "see {{nope}}"),
	}, localClauseFile("bad"))
}

func TestUnknownPlaceholderInADraftClauseStillFailsTheLoad(t *testing.T) {
	loadErr(t, map[string]string{
		globalClauseFile("bad"): clause("bad", 1, "claude", "see {{nope}}", "status: draft"),
	}, globalClauseFile("bad"))
}

// --- Loading: a clause is validated like the other Kinds ---

func TestClauseFrontmatterThatIsWrongFailsTheLoadNamingTheFile(t *testing.T) {
	for name, content := range map[string]string{
		"name does not match the file": clause("other", 1, "claude", "x"),
		"order is not a number":        "---\nname: bad\norder: first\ntargets: [claude]\n---\nx\n",
		"no frontmatter":               "just text {{cli}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			loadErr(t, map[string]string{globalClauseFile("bad"): content}, globalClauseFile("bad"))
		})
	}
}

// --- Digest ---

func TestDigestIsStableAndFollowsTheBlock(t *testing.T) {
	files := map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude, agents", "use {{mcp_name}} for {{project}}"),
	}
	b1, d1 := load(t, files).Instructions(govProject, "claude", testVars)
	b2, d2 := load(t, files).Instructions(govProject, "claude", testVars)
	if d1 == "" || b1 != b2 || d1 != d2 {
		t.Fatalf("same input gave (%q, %q) and (%q, %q); want equal and a non-empty digest", b1, d1, b2, d2)
	}
	changed := load(t, map[string]string{
		globalClauseFile("use"): clause("use", 1, "claude, agents", "use {{mcp_name}} for {{project}}!"),
	})
	if digestOf(changed, govProject, "claude", testVars) == d1 {
		t.Error("the digest did not change with the clause text")
	}
	snap := load(t, files)
	for name, got := range map[string]string{
		"project": digestOf(snap, "github.com/acme/other", "claude", testVars),
		"vars":    digestOf(snap, govProject, "claude", engine.InstructionVars{MCPName: "other", CLI: "goldie"}),
	} {
		if got == d1 {
			t.Errorf("the digest did not change with the %s, which changes the block", name)
		}
	}
}

func TestDigestOfTheDefaultBlockIsPresentAndStable(t *testing.T) {
	snap := load(t, map[string]string{"global/articles/a.md": article("A")})
	_, d1 := snap.Instructions("", "agents", testVars)
	_, d2 := snap.Instructions("", "agents", testVars)
	if d1 == "" || d1 != d2 {
		t.Errorf("default digests = %q and %q", d1, d2)
	}
}

// Issue #520: the body an author wrote is Source text an agent reads as
// instructions, so a hidden format character in it fails the load, naming the
// file and never echoing the body or the rune. Tabs and line breaks stay.
var bodyHiddenRunes = []rune{0x202e, 0x200b, 0xe0041, 0x1b}

func assertBodyRejection(t *testing.T, msg string, r rune) {
	t.Helper()
	if strings.ContainsRune(msg, r) {
		t.Errorf("error %q carries %U raw", msg, r)
	}
	if strings.Contains(msg, "SECRETBODY") {
		t.Errorf("error %q echoes the body", msg)
	}
}

func TestClauseBodyWithAHiddenRuneFailsTheLoadNamingTheFile(t *testing.T) {
	for _, r := range bodyHiddenRunes {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			body := "SECRETBODY visible" + string(r) + "hidden"
			msg := loadErr(t, map[string]string{
				globalClauseFile("ok"):  clause("ok", 1, "claude", "fine {{cli}}"),
				globalClauseFile("bad"): clause("bad", 2, "claude", body),
			}, globalClauseFile("bad"))
			if strings.Contains(msg, globalClauseFile("ok")) {
				t.Errorf("error names the good file too: %q", msg)
			}
			assertBodyRejection(t, msg, r)
		})
	}
}

func TestClauseBodyWithAHiddenRuneFailsTheLoadInEveryScopeAndStatus(t *testing.T) {
	body := "SECRETBODY a\u202eb"
	assertBodyRejection(t, loadErr(t, map[string]string{
		projectClauseFile("bad"): clause("bad", 1, "claude", body),
	}, projectClauseFile("bad")), 0x202e)
	assertBodyRejection(t, loadLocalErr(t, map[string]string{
		localClauseFile("bad"): clause("bad", 1, "claude", body),
	}, localClauseFile("bad")), 0x202e)
	assertBodyRejection(t, loadErr(t, map[string]string{
		globalClauseFile("bad"): clause("bad", 1, "claude", body, "status: draft"),
	}, globalClauseFile("bad")), 0x202e)
}

func TestClauseBodyOfVisibleTextTabsAndLineBreaksLoadsUnchanged(t *testing.T) {
	for name, body := range map[string]string{
		"visible with tab":    "first\tcolumn\nsecond line",
		"crlf line breaks":    "first line\r\nsecond line",
		"unicode and symbols": "caf\u00e9 \u2713 mid\u00a0dle",
	} {
		t.Run(name, func(t *testing.T) {
			snap := load(t, map[string]string{globalClauseFile("use"): clause("use", 1, "claude", body)})
			got, _ := snap.Instructions(govProject, "claude", testVars)
			// The block normalises CRLF to LF, so compare line by line.
			for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
				if !strings.Contains(got, line) {
					t.Errorf("block %q does not hold the line %q", got, line)
				}
			}
		})
	}
}
