package ui

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// Acceptance tests for #166 (the App shell in Vue, ADR 0010): what the web/
// source and the build wiring must hold. The rendered behaviour (the drawer,
// the nav, the keyboard path, axe) is in web/e2e and runs in CI's ux job; what
// a Go test can pin is the source tree and the way the SPA is served.
//
// These tests read web/ and run from the repository, like the .gitignore test.

const (
	webDir      = "../../web"
	emptyCopy   = "Nothing to show yet. Content appears here as goldie's pages come online."
	emptyCopyTy = "Nothing to show yet. Content appears here as goldie’s pages come online."
)

func readRepo(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// webFiles lists the files under web/ outside node_modules.
func webFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(webDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			out = append(out, filepath.ToSlash(p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWebIsAVueViteTypeScriptAppWithPinnedDependencies(t *testing.T) {
	pkg := readRepo(t, "../../web/package.json")
	for _, dep := range []string{`"vue"`, `"vite"`, `"typescript"`, `"@vitejs/plugin-vue"`} {
		if !strings.Contains(pkg, dep) {
			t.Errorf("web/package.json does not depend on %s", dep)
		}
	}
	for _, line := range strings.Split(pkg, "\n") {
		if strings.Contains(line, `"vue"`) || strings.Contains(line, `"vite"`) || strings.Contains(line, `"typescript"`) || strings.Contains(line, `"@vitejs/plugin-vue"`) {
			if regexp.MustCompile(`:\s*"([\^~><*]|latest)`).MatchString(line) {
				t.Errorf("web/package.json does not pin exactly: %s", strings.TrimSpace(line))
			}
		}
	}
	if !strings.Contains(pkg, `"build"`) {
		t.Error(`web/package.json has no "build" script (cmd_ui runs npm run build)`)
	}
	if _, err := os.Stat(webDir + "/index.html"); err != nil {
		t.Errorf("web/index.html: %v (the Vite entry; without it ./run ui builds nothing)", err)
	}
}

func TestIndexHTMLHasNoInlineScriptAndAnExternalPrePaintThemeScript(t *testing.T) {
	html := readRepo(t, "../../web/index.html")
	if !regexp.MustCompile(`(?i)<html[^>]*\blang="en"`).MatchString(html) {
		t.Error(`web/index.html: <html> has no lang="en"`)
	}
	scripts := regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`).FindAllStringSubmatch(html, -1)
	external := 0
	for _, s := range scripts {
		if !regexp.MustCompile(`(?i)\bsrc=`).MatchString(s[1]) || strings.TrimSpace(s[2]) != "" {
			t.Errorf("web/index.html has an inline script (CSP script-src 'self' blocks it): <script%s>", s[1])
		}
		if !strings.Contains(s[1], `type="module"`) {
			external++
			if regexp.MustCompile(`(?i)\b(async|defer)\b`).MatchString(s[1]) {
				t.Errorf("the pre-paint theme script must block, not async or defer: <script%s>", s[1])
			}
		}
	}
	if external == 0 {
		t.Error("web/index.html has no classic external <script src> for the pre-paint theme init")
	}
	if regexp.MustCompile(`(?i)\b(href|src)="(https?:)?//`).MatchString(html) {
		t.Error("web/index.html loads from another origin (no external requests)")
	}
	if regexp.MustCompile(`(?i)\sstyle=|\son[a-z]+=`).MatchString(html) {
		t.Error("web/index.html has an inline style or event handler attribute")
	}
	if strings.Count(strings.ToLower(html), "<h1") > 0 {
		t.Error("web/index.html has an h1: the one h1 belongs to the view, rendered by Vue")
	}
}

func TestWebSourceHasNoVHTMLNoInnerHTMLAndNoHexInComponents(t *testing.T) {
	hex := regexp.MustCompile(`#[0-9a-fA-F]{8}\b|#[0-9a-fA-F]{6}\b|#[0-9a-fA-F]{3,4}\b`)
	styleBlock := regexp.MustCompile(`(?is)<style\b[^>]*>(.*?)</style>`)
	var sources int
	for _, f := range webFiles(t) {
		if strings.Contains(f, "/e2e/") || strings.HasSuffix(f, "package-lock.json") {
			continue
		}
		ext := filepath.Ext(f)
		if ext != ".vue" && ext != ".ts" && ext != ".js" && ext != ".css" && ext != ".html" {
			continue
		}
		body := readRepo(t, f)
		if strings.HasPrefix(f, webDir+"/src/") {
			sources++
		}
		for _, bad := range []string{"v-html", "innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s uses %s (no raw HTML injection; ADR 0010)", f, bad)
			}
		}
		if regexp.MustCompile(`https?://`).MatchString(strings.ReplaceAll(body, "http://www.w3.org/", "")) && ext != ".html" {
			if !strings.HasSuffix(f, "vite.config.ts") {
				t.Errorf("%s names an external URL (no external requests)", f)
			}
		}
		base := filepath.Base(f)
		switch {
		case ext == ".vue":
			for _, m := range styleBlock.FindAllStringSubmatch(body, -1) {
				if h := hex.FindString(m[1]); h != "" {
					t.Errorf("%s has the hex colour %s: components use role tokens only", f, h)
				}
			}
		case ext == ".css" && base != "tokens.css" && base != "goldie.css":
			if h := hex.FindString(body); h != "" {
				t.Errorf("%s has the hex colour %s: only tokens.css and goldie.css hold values", f, h)
			}
		}
	}
	if sources == 0 {
		t.Error("web/src holds no source files")
	}
}

func TestTokensAndTheBrandLayerAreSeparateFiles(t *testing.T) {
	var tokens, goldie string
	for _, f := range webFiles(t) {
		switch filepath.Base(f) {
		case "tokens.css":
			tokens = f
		case "goldie.css":
			goldie = f
		}
	}
	if tokens == "" || goldie == "" {
		t.Fatalf("web/ must hold tokens.css (%q) and goldie.css (%q)", tokens, goldie)
	}
	brand := readRepo(t, goldie)
	if !strings.Contains(brand, ":root") {
		t.Error("goldie.css has no :root")
	}
	if !regexp.MustCompile(`\[data-theme=['"]dark['"]\]`).MatchString(brand) {
		t.Error(`goldie.css has no [data-theme='dark'] block`)
	}
	if !regexp.MustCompile(`prefers-color-scheme:\s*dark`).MatchString(brand) {
		t.Error("goldie.css has no prefers-color-scheme: dark block")
	}
	// An explicit data-theme wins over the system preference: the dark media
	// block must not apply when data-theme is set to light.
	if !regexp.MustCompile(`:root:not\(\[data-theme\]\)|:not\(\[data-theme=['"]light['"]\]\)|:root\[data-theme=['"]light['"]\]|\[data-theme=['"]light['"]\]`).MatchString(brand) {
		t.Error("goldie.css lets prefers-color-scheme: dark override an explicit data-theme=light")
	}
	for _, role := range []string{"--surface-1", "--fg-default", "--fg-muted", "--border-default", "--border-strong", "--brand", "--focus-ring"} {
		if !strings.Contains(brand, role) {
			t.Errorf("goldie.css defines no %s role token (§6.2)", role)
		}
	}
}

func TestFontsAreSelfHosted(t *testing.T) {
	var woff int
	for _, f := range webFiles(t) {
		if strings.HasSuffix(f, ".woff2") || strings.HasSuffix(f, ".woff") {
			woff++
		}
		if strings.HasSuffix(f, ".css") || strings.HasSuffix(f, ".vue") || strings.HasSuffix(f, ".html") {
			body := readRepo(t, f)
			if regexp.MustCompile(`(?i)fonts\.(googleapis|gstatic)\.com|@import\s+url\(\s*['"]?https?:`).MatchString(body) {
				t.Errorf("%s loads fonts from another origin", f)
			}
		}
	}
	if woff == 0 {
		t.Error("web/ holds no .woff2 font files: Inter, Space Grotesk and JetBrains Mono are self-hosted")
	}
}

func TestCmdUIInstallsWithoutScriptsAndNeverRunsNpx(t *testing.T) {
	run := readRepo(t, "../../run")
	start := strings.Index(run, "\ncmd_ui() {")
	if start < 0 {
		t.Fatal("run has no cmd_ui")
	}
	body := run[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "npm ci --ignore-scripts") {
		t.Error("cmd_ui must install with `npm ci --ignore-scripts` (#314)")
	}
	if strings.Contains(body, "npx vite") || regexp.MustCompile(`npx\s+(--no\s+)?vite`).MatchString(body) {
		t.Error("cmd_ui must not run `npx vite build`: npx could download an unpinned vite (#314)")
	}
	if !strings.Contains(body, "npm run build") && !strings.Contains(body, "npm exec --no -- vite build") {
		t.Error("cmd_ui must build with `npm run build` or `npm exec --no -- vite build` (#314)")
	}
}

func TestHandoffSection8HoldsTheEmptyStateCopy(t *testing.T) {
	doc := readRepo(t, "../../docs/design/ui-handoff.md")
	i := strings.Index(doc, "## 8. Copy")
	j := strings.Index(doc, "## 9. ")
	if i < 0 || j < i {
		t.Fatal("handoff §8 and §9 headings not found")
	}
	sec := doc[i:j]
	if !strings.Contains(sec, emptyCopy) && !strings.Contains(sec, emptyCopyTy) {
		t.Errorf("handoff §8 does not hold the empty-state copy %q (the copy is added in the same PR)", emptyCopy)
	}
}

// #473: the not-found copy and the operators-only 403 copy are in the §8 table
// with the strings the code uses. The not-found strings are read from the view
// itself, so the doc cannot drift from the code.
func TestHandoffSection8HoldsTheNotFoundAndOperatorsOnlyCopy(t *testing.T) {
	doc := readRepo(t, "../../docs/design/ui-handoff.md")
	i := strings.Index(doc, "## 8. Copy")
	j := strings.Index(doc, "## 9. ")
	if i < 0 || j < i {
		t.Fatal("handoff §8 and §9 headings not found")
	}
	// Typographic and plain apostrophes are the same string for the doc.
	sec := strings.NewReplacer("’", "'", "**", "").Replace(doc[i:j])

	view := readRepo(t, webDir+"/src/views/NotFoundView.vue")
	heading := regexp.MustCompile(`<h1>([^<]+)</h1>`).FindStringSubmatch(view)
	body := regexp.MustCompile(`<p>([^<]+)</p>`).FindStringSubmatch(view)
	link := regexp.MustCompile(`<RouterLink[^>]*>([^<]+)</RouterLink>`).FindStringSubmatch(view)
	if heading == nil || body == nil || link == nil {
		t.Fatalf("NotFoundView.vue has no h1, p and link to read the copy from: %v %v %v", heading, body, link)
	}
	for name, m := range map[string][]string{"h1": heading, "body": body, "link": link} {
		if s := strings.TrimSpace(m[1]); !strings.Contains(sec, s) {
			t.Errorf("handoff §8 does not hold the not-found %s copy %q the view uses", name, s)
		}
	}
	if heading[1] != "Page not found" {
		t.Errorf("the not-found h1 is %q, want %q", heading[1], "Page not found")
	}

	const forbidden = "This page is for operators."
	if !strings.Contains(sec, forbidden) {
		t.Errorf("handoff §8 does not hold the operators-only 403 copy %q", forbidden)
	}
	// They are table rows, not prose: a key and its copy on one row.
	for _, want := range []string{"Page not found", forbidden} {
		found := false
		for _, line := range strings.Split(sec, "\n") {
			if strings.HasPrefix(line, "|") && strings.Contains(line, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("handoff §8 has no copy table row holding %q", want)
		}
	}
}

// --- how the built shell is served ------------------------------------------

// shellFS is the shape of a Vite build of the shell: an index with the
// external pre-paint theme script, hashed assets, self-hosted fonts.
func shellFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                     {Data: []byte(`<!doctype html><html lang="en"><head><script src="/theme-init.js"></script><script type="module" src="/assets/index-abc.js"></script><link rel="stylesheet" href="/assets/index-abc.css"></head><body><div id="app"></div></body></html>`)},
		"theme-init.js":                  {Data: []byte("document.documentElement.dataset.theme='dark'")},
		"assets/index-abc.js":            {Data: []byte("1")},
		"assets/index-abc.css":           {Data: []byte("a{}")},
		"assets/inter-latin-400.woff2":   {Data: []byte("wOF2")},
		"assets/space-grotesk-600.woff2": {Data: []byte("wOF2")},
	}
}

func TestEveryRouteOfTheShellGetsTheIndexWith200AndNoLongCache(t *testing.T) {
	h := mustHandler(t, shellFS())
	// §2.2 routes, including /assets/:tab, which shares its prefix with the
	// hashed build output and must not be cached as immutable.
	for _, p := range []string{
		"/", "/signin", "/decisions", "/assets", "/assets/skills", "/assets/agents", "/assets/loops", "/assets/instructions",
		"/ops/activity", "/ops/sources", "/items/skill/some-name", "/no/such/route",
	} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200 (the shell shows its own not-found view)", p, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `<div id="app">`) {
			t.Errorf("GET %s: not the index page: %q", p, rec.Body)
		}
		wantHeader(t, p, rec, "Cache-Control", "no-cache")
		wantHeader(t, p, rec, "Content-Security-Policy", uiCSP)
	}
}

func TestThePrePaintThemeScriptIsServedAsAnExternalScriptUnderTheCSP(t *testing.T) {
	h := mustHandler(t, shellFS())
	rec := get(t, h, "/theme-init.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /theme-init.js: status %d", rec.Code)
	}
	wantHeader(t, "theme-init.js", rec, "Content-Type", "text/javascript; charset=utf-8")
	wantHeader(t, "theme-init.js", rec, "Content-Security-Policy", uiCSP)
	wantHeader(t, "theme-init.js", rec, "Cache-Control", "no-cache")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-") {
		t.Errorf("the CSP allows inline script or eval: %s", csp)
	}
	// The served index and the packaged CSP agree: the font and style sources are self.
	for _, want := range []string{"font-src 'self'", "style-src 'self'", "connect-src 'self'", "default-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("the CSP lacks %s: %s", want, csp)
		}
	}
	if rec := get(t, h, "/assets/inter-latin-400.woff2"); rec.Header().Get("Content-Type") != "font/woff2" {
		t.Errorf("a self-hosted font is served as %q, want font/woff2", rec.Header().Get("Content-Type"))
	}
}

func TestTheBuiltShellIfEmbeddedHasNoInlineScriptAndAnExternalThemeScript(t *testing.T) {
	if !Built() {
		t.Skip("the SPA is not built into this binary (./run ui); the source checks above cover web/index.html")
	}
	b, err := fs.ReadFile(Assets(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, s := range regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`).FindAllStringSubmatch(html, -1) {
		if !strings.Contains(s[1], "src=") || strings.TrimSpace(s[2]) != "" {
			t.Errorf("the built index.html has an inline script: <script%s>", s[1])
		}
	}
	if !regexp.MustCompile(`<script\b[^>]*\bsrc="[^"]+"[^>]*>`).MatchString(html) {
		t.Error("the built index.html has no external script")
	}
	if strings.Contains(html, "<h1") && strings.Contains(html, "UI not built") {
		t.Error("the embedded index is the placeholder")
	}
}
