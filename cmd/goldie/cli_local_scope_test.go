package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #390: a repository's .goldie/articles/ starts the
// embedded server with the local Scope (#389).
//
// Pinned by the issue and the product decisions on #10:
//   - With .goldie/articles/ at the repository root (found from the working
//     directory), search, lookup and status start the embedded server over
//     the content of `dir` in .goldie/config.yaml (relative to the root), or
//     of --dir, or over empty content, with the local Scope added.
//   - A .goldie/ without articles/ (what goldie init writes: only config.yaml
//     with project:) does not switch to local mode: the client uses the
//     server it is given (GOLDIE_ADDR), and reads no dir.
//   - On the embedded server the local Scope answers every request: with no
//     Project, a registered one and an unregistered one.
//   - dir is untrusted: relative, inside the root, opened through os.Root.
//     Anything else is an error that names .goldie/config.yaml and no host
//     path, symlink target or oversized value; nothing is printed.
//   - The same content prints the same bytes locally and remotely.
//
// Left open by the record, so not pinned here: GOLDIE_ADDR or --addr together
// with .goldie/articles/; GOLDIE_DIR next to config.yaml's dir; a repository
// root without .git; what a symlink or a file named .goldie/articles does
// beyond "serves nothing from it and leaks no path".

const (
	lsLocalOnlyBody = "LOCAL-ONLY-BODY"
	lsLocalDeploy   = "LOCAL-DEPLOY-BODY"
	lsOutsideSecret = "OUTSIDE-SECRET-BODY"
)

const (
	lsOnlyLocalItem = "---\nname: Only local\nkeywords: [onlylocal]\n---\n" + lsLocalOnlyBody + "\n"
	lsDeployItem    = "---\nname: Local deploy\nkeywords: [kubernetes]\n---\n" + lsLocalDeploy + "\n"
)

// lsAssertScopeThenNormalise checks that every result of the local search JSON
// has scope "local" and every result of the remote one has scope "global", that
// both hold the same non-zero number of results, and that this is the number of
// scope fields it replaces. It returns local with exactly those values
// replaced by "global".
func lsAssertScopeThenNormalise(t *testing.T, local, remote string) string {
	t.Helper()
	count := func(label, want, out string) int {
		var v struct {
			Results []struct {
				Scope string `json:"scope"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%s search JSON does not decode: %v", label, err)
		}
		for _, r := range v.Results {
			if r.Scope != want {
				t.Errorf("%s result has scope %q, want %q", label, r.Scope, want)
			}
		}
		return len(v.Results)
	}
	n := count("local", "local", local)
	if m := count("remote", "global", remote); n == 0 || m != n {
		t.Fatalf("local has %d results and remote %d, want the same non-zero number", n, m)
	}
	if got := strings.Count(local, `"scope": "local"`); got != n {
		t.Fatalf("found %d scope fields to normalise, want %d", got, n)
	}
	return strings.ReplaceAll(local, `"scope": "local"`, `"scope": "global"`)
}

// lsContentDir is the example content, absolute because lsRun changes the
// working directory.
var lsContentDir = func() string {
	d, err := filepath.Abs(filepath.Join("..", "..", "internal", "app", "testdata", "content"))
	if err != nil {
		panic(err)
	}
	return d
}()

// lsRepo writes files (slash paths) under a new repository root, which has a
// .git directory, and returns the root.
func lsRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	lsWrite(t, root, files)
	return root
}

func lsWrite(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// lsOutside is a directory outside every repository, holding a content tree
// with one global Item that must never be served.
func lsOutside(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	lsWrite(t, d, map[string]string{
		"global/articles/secret.md": "---\nname: Secret\nkeywords: [secret]\n---\n" + lsOutsideSecret + "\n",
	})
	return d
}

// lsCopyContent copies the example content into root/name.
func lsCopyContent(t *testing.T, root, name string) {
	t.Helper()
	if err := os.CopyFS(filepath.Join(root, name), os.DirFS(lsContentDir)); err != nil {
		t.Fatal(err)
	}
}

// lsRun runs the CLI with dir as the working directory and env set. The
// client variables are cleared first.
func lsRun(t *testing.T, dir string, env map[string]string, args ...string) (string, error) {
	t.Helper()
	for _, name := range []string{"GOLDIE_ADDR", "GOLDIE_TOKEN", "GOLDIE_TOKEN_FILE", "GOLDIE_PROJECT", "GOLDIE_DIR"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	t.Chdir(dir)
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

func lsMustRun(t *testing.T, dir string, env map[string]string, args ...string) string {
	t.Helper()
	out, err := lsRun(t, dir, env, args...)
	if err != nil {
		t.Fatalf("goldie %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// lsNoLeak fails when msg holds any of the host paths, or a control
// character, or is longer than a short error should be.
func lsNoLeak(t *testing.T, msg string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if p != "" && strings.Contains(msg, p) {
			t.Errorf("error %q leaks %q", msg, p)
		}
	}
	for _, r := range msg {
		if unicode.IsControl(r) {
			t.Errorf("error carries the control character %U: %q", r, msg)
			break
		}
	}
	if len(msg) > 1000 {
		t.Errorf("error is %d bytes: it carries something it should not", len(msg))
	}
}

// lsRealPaths are root and its resolved form, which is what an error would
// show if it leaked the path through a symlinked temporary directory.
func lsRealPaths(t *testing.T, root string) []string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return []string{root, resolved, filepath.Dir(root)}
}

// .goldie/articles/ switches the client commands to the embedded server with
// the local Scope: over empty content, over config.yaml's dir, over --dir.
func TestCLILocalScopeStartsTheEmbeddedServer(t *testing.T) {
	type tc struct {
		name  string
		files map[string]string
		args  []string
		cwd   string // relative to the root
		// wantGlobal: the example content's agent-loops Item is served too.
		wantGlobal bool
	}
	local := map[string]string{".goldie/articles/team/only-local.md": lsOnlyLocalItem}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range local {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []tc{
		{name: "empty content", files: local},
		{name: "empty content, config.yaml with project only", files: with(map[string]string{".goldie/config.yaml": "project: github.com/acme/shop\n"})},
		{name: "config.yaml dir", files: with(map[string]string{".goldie/config.yaml": "dir: content\n"}), wantGlobal: true},
		{name: "config.yaml dir with project", files: with(map[string]string{".goldie/config.yaml": "project: github.com/acme/shop\ndir: content\n"}), wantGlobal: true},
		{name: "config.yaml dir in a subdirectory", files: with(map[string]string{".goldie/config.yaml": "dir: sub/content\n"}), wantGlobal: true},
		{name: "--dir", files: local, args: []string{"--dir", lsContentDir}, wantGlobal: true},
		{name: "--dir over config.yaml dir", files: with(map[string]string{".goldie/config.yaml": "dir: nowhere\n"}), args: []string{"--dir", lsContentDir}, wantGlobal: true},
		{name: "from a subdirectory of the repository", files: with(map[string]string{".goldie/config.yaml": "dir: content\n"}), cwd: "deep/er", wantGlobal: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := lsRepo(t, c.files)
			if strings.Contains(c.files[".goldie/config.yaml"], "dir: content") {
				lsCopyContent(t, root, "content")
			}
			if strings.Contains(c.files[".goldie/config.yaml"], "dir: sub/content") {
				lsCopyContent(t, root, "sub/content")
			}
			cwd := filepath.Join(root, filepath.FromSlash(c.cwd))
			if err := os.MkdirAll(cwd, 0o750); err != nil {
				t.Fatal(err)
			}
			out := lsMustRun(t, cwd, nil, append([]string{"lookup", "team/only-local"}, c.args...)...)
			if !strings.Contains(out, lsLocalOnlyBody) {
				t.Errorf("lookup of the local Item lacks its body:\n%s", out)
			}
			out = lsMustRun(t, cwd, nil, append([]string{"search", "onlylocal"}, c.args...)...)
			if !strings.Contains(out, "team/only-local") {
				t.Errorf("search lacks the local Item:\n%s", out)
			}
			out = lsMustRun(t, cwd, nil, append([]string{"status"}, c.args...)...)
			if !strings.Contains(out, "Server: reachable") || !strings.Contains(out, "Auth: token accepted") {
				t.Errorf("status does not report the embedded server:\n%s", out)
			}
			_, err := lsRun(t, cwd, nil, append([]string{"lookup", "agent-loops"}, c.args...)...)
			if got := err == nil; got != c.wantGlobal {
				t.Errorf("lookup of a global Item succeeded = %v, want %v (err %v)", got, c.wantGlobal, err)
			}
		})
	}
}

// An existing GOLDIE_TOKEN or a wrong one does not matter in local mode: the
// embedded server makes its own token.
func TestCLILocalScopeIgnoresTheRemoteToken(t *testing.T) {
	root := lsRepo(t, map[string]string{".goldie/articles/team/only-local.md": lsOnlyLocalItem})
	out := lsMustRun(t, root, map[string]string{"GOLDIE_TOKEN": "wrong-token-0123456789abcdef0123456789"}, "lookup", "team/only-local")
	if !strings.Contains(out, lsLocalOnlyBody) {
		t.Errorf("lookup lacks the local body:\n%s", out)
	}
}

// A .goldie/ without articles/ does not switch to local mode: the client keeps
// using the server it is given, and reads no dir from config.yaml.
func TestCLIGoldieWithoutArticlesStaysRemote(t *testing.T) {
	srv := cliServer(t)
	env := map[string]string{"GOLDIE_ADDR": srv.URL, "GOLDIE_TOKEN": cliToken}
	outside := lsOutside(t)
	cases := []struct {
		name    string
		files   map[string]string
		project string // the Project config.yaml names, which the remote request carries
	}{
		{"config.yaml with project only (goldie init)", map[string]string{".goldie/config.yaml": "project: github.com/acme/shop\n"}, registeredProject},
		{"config.yaml with project and a dir", map[string]string{".goldie/config.yaml": "project: github.com/acme/shop\ndir: content\n"}, registeredProject},
		{"config.yaml with a dir", map[string]string{".goldie/config.yaml": "dir: content\n"}, ""},
		{"config.yaml with a hostile dir", map[string]string{".goldie/config.yaml": "dir: \"" + outside + "\"\n"}, ""},
		{"config.yaml with a non-string dir", map[string]string{".goldie/config.yaml": "dir: [a, b]\n"}, ""},
		{"an empty .goldie directory", map[string]string{".goldie/.keep": ""}, ""},
		{"articles elsewhere in .goldie", map[string]string{".goldie/other/team/only-local.md": lsOnlyLocalItem}, ""},
		{"no .goldie at all", map[string]string{"README.md": "x\n"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var extra []string
			if c.project != "" {
				extra = []string{"--project", c.project}
			}
			// What the remote server answers for this Project, asked from a
			// directory that has no .goldie.
			want := lsMustRun(t, t.TempDir(), env, append([]string{"lookup", "platform/deploy"}, extra...)...)
			wantSearch := lsMustRun(t, t.TempDir(), env, append([]string{"search", "kubernetes"}, extra...)...)
			if !strings.Contains(want, "deploy") {
				t.Fatalf("remote lookup is not an answer:\n%s", want)
			}
			root := lsRepo(t, c.files)
			lsCopyContent(t, root, "content")
			got, err := lsRun(t, root, env, "lookup", "platform/deploy")
			if err != nil || got != want {
				t.Errorf("lookup: err = %v, output\n%s\nwant the remote server's\n%s", err, got, want)
			}
			got, err = lsRun(t, root, env, "search", "kubernetes")
			if err != nil || got != wantSearch {
				t.Errorf("search: err = %v, output\n%s\nwant the remote server's\n%s", err, got, wantSearch)
			}
		})
	}
}

// Without a server address, a repository without .goldie/articles/ is still
// remote mode, with its usual error: nothing was started.
func TestCLIGoldieWithoutArticlesAndWithoutAnAddressIsTheUsualError(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"project only": {".goldie/config.yaml": "project: github.com/acme/shop\n"},
		"no .goldie":   {"README.md": "x\n"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := lsRun(t, lsRepo(t, files), nil, "lookup", "team/only-local")
			if err == nil || !strings.Contains(err.Error(), "no server address") {
				t.Errorf("err = %v, want the missing-address error", err)
			}
			if out != "" {
				t.Errorf("output on failure: %q", out)
			}
		})
	}
}

// A .goldie/articles that is a file or a symlink serves nothing from what it
// points at, and no error or output carries a host path.
func TestCLIGoldieArticlesThatIsNotADirectoryServesNothing(t *testing.T) {
	outside := lsOutside(t)
	lsWrite(t, outside, map[string]string{"articles/secret.md": "---\nname: Secret\nkeywords: [secret]\n---\n" + lsOutsideSecret + "\n"})
	for name, setup := range map[string]func(t *testing.T, root string){
		"a file": func(t *testing.T, root string) {
			lsWrite(t, root, map[string]string{".goldie/articles": "not a directory\n"})
		},
		"a symlink to a directory outside": func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, ".goldie"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "articles"), filepath.Join(root, ".goldie", "articles")); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
		},
		"a symlink to a directory inside": func(t *testing.T, root string) {
			lsWrite(t, root, map[string]string{"real/team/only-local.md": lsOnlyLocalItem})
			if err := os.MkdirAll(filepath.Join(root, ".goldie"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, ".goldie", "articles")); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := lsRepo(t, nil)
			setup(t, root)
			for _, args := range [][]string{{"lookup", "secret"}, {"search", "secret"}, {"lookup", "team/only-local"}} {
				out, err := lsRun(t, root, nil, args...)
				if strings.Contains(out, lsOutsideSecret) || strings.Contains(out, lsLocalOnlyBody) {
					t.Errorf("%v served content through the articles path:\n%s", args, out)
				}
				if err != nil {
					lsNoLeak(t, err.Error(), append(lsRealPaths(t, root), outside)...)
				}
			}
		})
	}
}

// The local Scope answers every request of the embedded server: a request
// with the Project from config.yaml, with --project, with GOLDIE_PROJECT, with
// an unregistered Project and with none. The Item that exists only locally
// and the one that shadows the global and the Project copy answer in all of
// them.
func TestCLILocalScopeAnswersEveryRequest(t *testing.T) {
	files := map[string]string{
		".goldie/articles/team/only-local.md": lsOnlyLocalItem,
		".goldie/articles/platform/deploy.md": lsDeployItem,
	}
	withProject := lsRepo(t, map[string]string{
		".goldie/articles/team/only-local.md": lsOnlyLocalItem,
		".goldie/articles/platform/deploy.md": lsDeployItem,
		".goldie/config.yaml":                 "project: " + registeredProject + "\n",
	})
	withoutProject := lsRepo(t, files)
	content := lsContentDir
	for name, c := range map[string]struct {
		root string
		env  map[string]string
		args []string
	}{
		"Project from config.yaml":         {withProject, nil, nil},
		"--project registered":             {withoutProject, nil, []string{"--project", registeredProject}},
		"--project unregistered":           {withoutProject, nil, []string{"--project", unregisteredProject}},
		"--project unregistered in config": {withProject, nil, []string{"--project", unregisteredProject}},
		"GOLDIE_PROJECT registered":        {withoutProject, map[string]string{"GOLDIE_PROJECT": registeredProject}, nil},
		"GOLDIE_PROJECT unregistered":      {withoutProject, map[string]string{"GOLDIE_PROJECT": unregisteredProject}, nil},
		"no Project":                       {withoutProject, nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"--dir", content}, c.args...)
			out := lsMustRun(t, c.root, c.env, append([]string{"lookup", "team/only-local"}, args...)...)
			if !strings.Contains(out, lsLocalOnlyBody) {
				t.Errorf("lookup of the local-only Item lacks its body:\n%s", out)
			}
			out = lsMustRun(t, c.root, c.env, append([]string{"search", "onlylocal"}, args...)...)
			if !strings.Contains(out, "team/only-local") {
				t.Errorf("search lacks the local-only Item:\n%s", out)
			}
			out = lsMustRun(t, c.root, c.env, append([]string{"lookup", "platform/deploy"}, args...)...)
			if !strings.Contains(out, lsLocalDeploy) {
				t.Errorf("lookup of the shadowed Item lacks the local body:\n%s", out)
			}
			for _, bad := range []string{"Use the platform chart", "Shop-specific deploy"} {
				if strings.Contains(out, bad) {
					t.Errorf("the %q copy answered over the local one:\n%s", bad, out)
				}
			}
			out = lsMustRun(t, c.root, c.env, append([]string{"search", "kubernetes", "--json"}, args...)...)
			if !strings.Contains(out, "platform/deploy") {
				t.Errorf("search lacks the shadowing Item:\n%s", out)
			}
		})
	}
}

// A local Item that shadows an enforced Item fails the embedded server's
// start, and the error names the file relative to the repository root and no
// host path. Nothing is printed.
func TestCLILocalScopeShadowingAnEnforcedItemFailsWithoutAPath(t *testing.T) {
	content := t.TempDir()
	lsWrite(t, content, map[string]string{
		"global/articles/rules.md": "---\nname: Rules\nkeywords: [rules]\nenforced: true\n---\nThe rules.\n",
	})
	root := lsRepo(t, map[string]string{
		".goldie/articles/rules.md": "---\nname: Mine\nkeywords: [rules]\n---\nMy rules.\n",
	})
	out, err := lsRun(t, root, nil, "lookup", "rules", "--dir", content)
	if err == nil {
		t.Fatalf("lookup succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), ".goldie/articles/rules.md") || !strings.Contains(err.Error(), "article:rules") {
		t.Errorf("error %q does not name the local file and the Item", err)
	}
	lsNoLeak(t, err.Error(), append(lsRealPaths(t, root), content)...)
	if out != "" {
		t.Errorf("output on failure: %q", out)
	}
}

// dir in config.yaml is untrusted. Each of these is refused with an error that
// names .goldie/config.yaml, and carries no absolute path, no symlink target
// and no oversized value. Nothing is printed, and nothing outside the
// repository is served.
func TestCLILocalScopeRefusesAHostileDir(t *testing.T) {
	outside := lsOutside(t)
	for name, mk := range map[string]func(t *testing.T, root string) (yamlValue string){
		"absolute path": func(t *testing.T, root string) string { return `"` + outside + `"` },
		"absolute path to the root itself": func(t *testing.T, root string) string {
			return `"` + root + `"`
		},
		"parent directory": func(t *testing.T, root string) string { return `".."` },
		"dot dot escape": func(t *testing.T, root string) string {
			rel, err := filepath.Rel(root, outside)
			if err != nil {
				t.Fatal(err)
			}
			return `"` + filepath.ToSlash(rel) + `"`
		},
		"dot dot escape behind a dot segment": func(t *testing.T, root string) string {
			rel, err := filepath.Rel(root, outside)
			if err != nil {
				t.Fatal(err)
			}
			return `"./a/../` + filepath.ToSlash(rel) + `"`
		},
		"dot dot after a name": func(t *testing.T, root string) string {
			rel, err := filepath.Rel(root, outside)
			if err != nil {
				t.Fatal(err)
			}
			return `"content/../../` + filepath.ToSlash(rel) + `"`
		},
		"symlink to a directory outside": func(t *testing.T, root string) string {
			if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			return `link`
		},
		"symlink in the middle of the path": func(t *testing.T, root string) string {
			if err := os.MkdirAll(filepath.Join(root, "real"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "real", "link")); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			return `real/link`
		},
		"symlink in the middle, content below it": func(t *testing.T, root string) string {
			if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			return `link/global`
		},
		"empty string":         func(t *testing.T, root string) string { return `""` },
		"integer":              func(t *testing.T, root string) string { return `5` },
		"boolean":              func(t *testing.T, root string) string { return `true` },
		"float":                func(t *testing.T, root string) string { return `3.5` },
		"list":                 func(t *testing.T, root string) string { return `[a, b]` },
		"mapping":              func(t *testing.T, root string) string { return `{a: b}` },
		"missing directory":    func(t *testing.T, root string) string { return `no-such-directory` },
		"a file":               func(t *testing.T, root string) string { return `README.md` },
		"the config file":      func(t *testing.T, root string) string { return `.goldie/config.yaml` },
		"a local article file": func(t *testing.T, root string) string { return `.goldie/articles/team/only-local.md` },
		"NUL byte":             func(t *testing.T, root string) string { return `"a\x00b"` },
		"newline":              func(t *testing.T, root string) string { return `"a\nb"` },
		"escape sequence":      func(t *testing.T, root string) string { return `"a\e[31mb"` },
		"very long path":       func(t *testing.T, root string) string { return `"` + strings.Repeat("a/", 3000) + `"` },
		"very long name":       func(t *testing.T, root string) string { return `"` + strings.Repeat("a", 5000) + `"` },
	} {
		t.Run(name, func(t *testing.T) {
			root := lsRepo(t, map[string]string{
				".goldie/articles/team/only-local.md": lsOnlyLocalItem,
				"README.md":                           "readme\n",
			})
			value := mk(t, root)
			lsWrite(t, root, map[string]string{".goldie/config.yaml": "dir: " + value + "\n"})
			for _, args := range [][]string{{"lookup", "secret"}, {"search", "secret"}, {"status"}, {"lookup", "team/only-local"}} {
				out, err := lsRun(t, root, nil, args...)
				if err == nil {
					t.Fatalf("goldie %s succeeded with dir: %s:\n%s", strings.Join(args, " "), value, out)
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
					t.Errorf("error echoes the dir value")
				}
			}
		})
	}
}

// --dir takes the place of config.yaml's dir, so a dir that would be refused
// is not read when --dir is given.
func TestCLILocalScopeFlagDirReplacesAHostileConfigDir(t *testing.T) {
	root := lsRepo(t, map[string]string{
		".goldie/articles/team/only-local.md": lsOnlyLocalItem,
		".goldie/config.yaml":                 "dir: \"" + lsOutside(t) + "\"\n",
	})
	out := lsMustRun(t, root, nil, "lookup", "team/only-local", "--dir", lsContentDir)
	if !strings.Contains(out, lsLocalOnlyBody) {
		t.Errorf("lookup lacks the local body:\n%s", out)
	}
	if strings.Contains(out, lsOutsideSecret) {
		t.Errorf("content from outside the repository was served:\n%s", out)
	}
}

// config.yaml's dir is a path inside the repository: nested directories and a
// symlink that stays inside the root are not the attack; a plain nested dir
// works, which pins that the check is not "no slashes".
func TestCLILocalScopeAcceptsANestedRelativeDir(t *testing.T) {
	root := lsRepo(t, map[string]string{
		".goldie/articles/team/only-local.md": lsOnlyLocalItem,
		".goldie/config.yaml":                 "dir: docs/golden/content\n",
	})
	lsCopyContent(t, root, "docs/golden/content")
	out := lsMustRun(t, root, nil, "lookup", "agent-loops")
	if !strings.Contains(out, "agent-loops") {
		t.Errorf("lookup of a global Item over the nested dir:\n%s", out)
	}
}

// The same content prints the same bytes through the embedded server with the
// local Scope as through a remote server that holds the Items as global ones:
// text and --json, an answer, the empty result and status.
func TestCLILocalScopeOutputMatchesRemoteMode(t *testing.T) {
	remoteContent := t.TempDir()
	if err := os.CopyFS(remoteContent, os.DirFS(lsContentDir)); err != nil {
		t.Fatal(err)
	}
	lsWrite(t, remoteContent, map[string]string{"global/articles/team/only-local.md": lsOnlyLocalItem})
	h, err := app.PublicHandler(platform.Config{ContentDir: remoteContent, APIToken: cliToken, RateLimit: platform.DefaultRateLimit()}, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	root := lsRepo(t, map[string]string{
		".goldie/articles/team/only-local.md": lsOnlyLocalItem,
		".goldie/config.yaml":                 "dir: content\n",
	})
	lsCopyContent(t, root, "content")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"search", []string{"search", "kubernetes"}, "platform/deploy"},
		{"search local", []string{"search", "onlylocal"}, "team/only-local"},
		{"search json", []string{"search", "onlylocal", "--json"}, "team/only-local"},
		{"search none", []string{"search", "zzzznothing"}, ""},
		{"lookup", []string{"lookup", "platform/deploy"}, "deploy"},
		{"lookup local", []string{"lookup", "team/only-local"}, lsLocalOnlyBody},
		{"lookup local json", []string{"lookup", "team/only-local", "--json"}, "team/only-local"},
		{"status", []string{"status"}, "Server: reachable"},
		{"status json", []string{"status", "--json"}, "tokenAccepted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			remote := lsMustRun(t, t.TempDir(), nil, append(append([]string{}, c.args...), "--addr", srv.URL, "--token", cliToken)...)
			local := lsMustRun(t, root, nil, c.args...)
			remote = strings.ReplaceAll(remote, srv.URL, "")
			if c.name == "search json" {
				// The one difference that is the point of the local Scope: the
				// same Item is "local" here and "global" on the remote server.
				// Both are asserted, then only that field's value is made equal;
				// every other byte is still compared.
				local = lsAssertScopeThenNormalise(t, local, remote)
			}
			if c.name == "lookup local json" {
				// The article response names its Scope too (#577): the same
				// difference as above, asserted before it is normalised.
				if !strings.Contains(local, `"scope": "local"`) || !strings.Contains(remote, `"scope": "global"`) {
					t.Errorf("lookup JSON lacks its scope: local %q, remote %q", local, remote)
				}
				local = strings.ReplaceAll(local, `"scope": "local"`, `"scope": "global"`)
			}
			if local != remote {
				t.Errorf("local output differs from remote\n--- local ---\n%s\n--- remote ---\n%s", local, remote)
			}
			if !strings.Contains(local, c.want) {
				t.Errorf("local output lacks %q:\n%s", c.want, local)
			}
		})
	}
}
