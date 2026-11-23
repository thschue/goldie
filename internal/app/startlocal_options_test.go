package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #390: StartLocal takes a small options value, not a bare directory:
//
//	type LocalOptions struct {
//	    BaseDir   string // the content directory (global/articles, projects/...); "" is empty content
//	    GoldieDir string // the repository's .goldie directory, the local Scope; "" is none
//	}
//	func StartLocal(ctx context.Context, opts LocalOptions) (addr, token string, stop func(), err error)
//
// With a GoldieDir the embedded server serves the local Scope (#389) over the
// base content, and the local Scope answers every request: with no Project,
// with a registered one and with one that is not registered (PM, #10, tier 2).
// A local Item shadows a global or Project Item of the same name. A load error
// names a file relative to the repository root (.goldie/...), never a host
// path. Everything StartLocal did with a bare directory it does with BaseDir.

const (
	optsRegistered   = "github.com/acme/shop"
	optsUnregistered = "github.com/acme/nothere"
	optsLocalOnly    = "/api/v1/articles/team%2Fonly-local"
	optsDeployPath   = "/api/v1/articles/platform%2Fdeploy"
)

// optsWriteTree writes files (slash paths) under a new temporary directory.
func optsWriteTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// optsGet is an authenticated GET and returns the status and body.
func optsGet(t *testing.T, addr, token, path string) (int, string) {
	t.Helper()
	resp, body := rawDo(t, http.MethodGet, addr+path, bearer(token))
	return resp.StatusCode, string(body)
}

// optsLocalRepo is a repository with two local Items: one that exists only
// locally and one that shadows platform/deploy, which the base content holds
// as a global Item and as the registered Project's.
func optsLocalRepo(t *testing.T) string {
	t.Helper()
	return optsWriteTree(t, map[string]string{
		".goldie/articles/team/only-local.md": "---\nname: Only local\nkeywords: [onlylocal]\n---\nLOCAL-ONLY-BODY\n",
		".goldie/articles/platform/deploy.md": "---\nname: Local deploy\nkeywords: [kubernetes]\n---\nLOCAL-DEPLOY-BODY\n",
	})
}

func optsStart(t *testing.T, opts LocalOptions) (addr, token string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addr, token, stop, err := StartLocal(ctx, opts)
	if err != nil {
		t.Fatalf("StartLocal(%+v): %v", opts, err)
	}
	t.Cleanup(stop)
	return addr, token
}

// The local Scope answers in every view of the embedded server: no Project, a
// registered Project and one that is not registered.
func TestStartLocalWithAGoldieDirAnswersTheLocalScopeInEveryView(t *testing.T) {
	repo := optsLocalRepo(t)
	addr, token := optsStart(t, LocalOptions{BaseDir: testContent, GoldieDir: filepath.Join(repo, ".goldie")})
	for name, query := range map[string]string{
		"no project":           "",
		"registered project":   "?project=" + optsRegistered,
		"unregistered project": "?project=" + optsUnregistered,
	} {
		t.Run(name, func(t *testing.T) {
			code, body := optsGet(t, addr, token, optsLocalOnly+query)
			if code != http.StatusOK || !strings.Contains(body, "LOCAL-ONLY-BODY") {
				t.Errorf("GET local-only Item = %d %q, want 200 with its body", code, body)
			}
			code, body = optsGet(t, addr, token, optsDeployPath+query)
			if code != http.StatusOK || !strings.Contains(body, "LOCAL-DEPLOY-BODY") {
				t.Errorf("GET shadowed Item = %d %q, want 200 with the local body", code, body)
			}
			if strings.Contains(body, "Use the platform chart") || strings.Contains(body, "Shop-specific deploy") {
				t.Errorf("the global or Project copy answered over the local one: %q", body)
			}
			code, body = optsGet(t, addr, token, "/api/v1/search?q=onlylocal"+strings.Replace(query, "?", "&", 1))
			if code != http.StatusOK || !strings.Contains(body, "team/only-local") {
				t.Errorf("search = %d %q, want the local Item", code, body)
			}
		})
	}
}

// The base content is still served next to the local Scope.
func TestStartLocalWithAGoldieDirStillServesTheBaseContent(t *testing.T) {
	repo := optsLocalRepo(t)
	addr, token := optsStart(t, LocalOptions{BaseDir: testContent, GoldieDir: filepath.Join(repo, ".goldie")})
	if code, body := optsGet(t, addr, token, "/api/v1/articles/agent-loops"); code != http.StatusOK {
		t.Errorf("GET global Item = %d %q, want 200", code, body)
	}
}

// An empty BaseDir is empty content: only the local Scope is served.
func TestStartLocalWithAGoldieDirOverEmptyContent(t *testing.T) {
	repo := optsLocalRepo(t)
	addr, token := optsStart(t, LocalOptions{GoldieDir: filepath.Join(repo, ".goldie")})
	if code, body := optsGet(t, addr, token, optsLocalOnly); code != http.StatusOK || !strings.Contains(body, "LOCAL-ONLY-BODY") {
		t.Errorf("GET local Item = %d %q, want 200", code, body)
	}
	if code, _ := optsGet(t, addr, token, "/api/v1/articles/agent-loops"); code != http.StatusNotFound {
		t.Errorf("GET a global Item over empty content = %d, want 404", code)
	}
}

// Without a GoldieDir nothing local is served: the local Scope exists only
// where the caller found a .goldie and handed it over.
func TestStartLocalWithoutAGoldieDirServesNoLocalScope(t *testing.T) {
	_ = optsLocalRepo(t) // on disk, but not handed over
	addr, token := optsStart(t, LocalOptions{BaseDir: testContent})
	if code, _ := optsGet(t, addr, token, optsLocalOnly); code != http.StatusNotFound {
		t.Errorf("GET a local Item without a GoldieDir = %d, want 404", code)
	}
	if code, body := optsGet(t, addr, token, optsDeployPath); code != http.StatusOK || strings.Contains(body, "LOCAL-DEPLOY-BODY") {
		t.Errorf("GET shadowed Item = %d %q, want the global copy", code, body)
	}
}

// A GoldieDir that does not exist is an empty local Scope (#389).
func TestStartLocalWithAMissingGoldieDirIsAnEmptyLocalScope(t *testing.T) {
	repo := t.TempDir()
	addr, token := optsStart(t, LocalOptions{BaseDir: testContent, GoldieDir: filepath.Join(repo, ".goldie")})
	if code, _ := optsGet(t, addr, token, "/api/v1/articles/agent-loops"); code != http.StatusOK {
		t.Errorf("GET global Item = %d, want 200", code)
	}
}

// A local file that does not load fails StartLocal with the content-load
// prefix and the file relative to the repository root, and no host path.
func TestStartLocalWithABadLocalFileNamesItRelativeToTheRepository(t *testing.T) {
	enforced := optsWriteTree(t, map[string]string{
		"global/articles/rules.md": "---\nname: Rules\nkeywords: [rules]\nenforced: true\n---\nThe rules.\n",
	})
	for name, tc := range map[string]struct {
		base  string
		files map[string]string
		want  []string
	}{
		"no frontmatter": {testContent, map[string]string{".goldie/articles/bad.md": "no frontmatter here\n"}, []string{".goldie/articles/bad.md"}},
		"shadows an enforced Item": {enforced, map[string]string{
			".goldie/articles/rules.md": "---\nname: Mine\nkeywords: [rules]\n---\nMy rules.\n",
		}, []string{".goldie/articles/rules.md", "article:rules"}},
		"unknown config key": {testContent, map[string]string{".goldie/config.yaml": "bogus: 1\n"}, []string{".goldie/config.yaml"}},
	} {
		t.Run(name, func(t *testing.T) {
			repo := optsWriteTree(t, tc.files)
			addr, token, stop, err := StartLocal(context.Background(), LocalOptions{BaseDir: tc.base, GoldieDir: filepath.Join(repo, ".goldie")})
			if err == nil {
				if stop != nil {
					stop()
				}
				t.Fatal("StartLocal succeeded, want an error")
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, startLocalErrPrefix) {
				t.Errorf("error %q lacks the prefix %q", msg, startLocalErrPrefix)
			}
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error %q does not name %s", msg, w)
				}
			}
			for _, p := range []string{repo, tc.base, filepath.Dir(repo)} {
				if strings.Contains(msg, p) {
					t.Errorf("error %q leaks the host path %s", msg, p)
				}
			}
			if addr != "" || token != "" {
				t.Errorf("addr %q and a token of %d bytes returned with an error", addr, len(token))
			}
		})
	}
}

// A GoldieDir that names no directory of its own (the file system root, a
// path ending in . or ..) is refused, without the path in the error.
func TestStartLocalRefusesAGoldieDirThatNamesNoDirectory(t *testing.T) {
	repo := t.TempDir()
	for name, dir := range map[string]string{
		"root":    string(filepath.Separator),
		"dot dot": repo + string(filepath.Separator) + "..",
		"dot":     repo + string(filepath.Separator) + ".",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, stop, err := StartLocal(context.Background(), LocalOptions{BaseDir: testContent, GoldieDir: dir})
			if err == nil {
				if stop != nil {
					stop()
				}
				t.Fatalf("StartLocal accepted GoldieDir %q", dir)
			}
			if len(dir) > 1 && strings.Contains(err.Error(), dir) {
				t.Errorf("error %q echoes the path %q", err, dir)
			}
		})
	}
}
