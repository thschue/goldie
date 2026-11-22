package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// FuzzNormaliseRemote checks that no input panics and that an accepted result
// is a clean host/path that never carries userinfo.
func FuzzNormaliseRemote(f *testing.F) {
	for _, s := range []string{"git@github.com:a/b.git", "https://u:p@h/a/b", "ssh://git@h:22/a/b", "", ":", "@:/", "a:b@c:d"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		id, ok := NormaliseRemote(in)
		if !ok {
			return
		}
		if strings.ContainsAny(id, "@ \t\n") || !validProjectID(id) || id != strings.ToLower(id) {
			t.Errorf("NormaliseRemote(%q) = %q", in, id)
		}
	})
}

// FuzzProjectFiles feeds hostile bytes to both project files a checkout can
// carry: the git config parser and .goldie/config.yaml. Neither may panic or
// hang, and a Project that comes out must pass the segment rules.
func FuzzProjectFiles(f *testing.F) {
	for _, s := range []string{
		"[remote \"origin\"]\n\turl = git@github.com:a/b.git\n",
		"[remote \"upstream\"\n url=\n[remote \"\"]\nurl = \"\n",
		"project: github.com/a/b\n",
		"a: &a [x,x,x,x,x,x,x,x]\nb: &b [*a,*a,*a,*a,*a,*a,*a,*a]\nc: &c [*b,*b,*b,*b,*b,*b,*b,*b]\nproject: *c\n",
		"project: !!binary aGk=\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, u := range remoteURLs(in) {
			if id, ok := NormaliseRemote(u); ok && !validProjectID(id) {
				t.Errorf("remote %q gave %q", u, id)
			}
		}
		dir := t.TempDir()
		write(t, filepath.Join(dir, ".goldie", "config.yaml"), in)
		if p, err := ResolveProject(dir, "", ""); err == nil && p.ID != "" && !validProjectID(p.ID) {
			t.Errorf("config.yaml %q gave %q", in, p.ID)
		}
	})
}
