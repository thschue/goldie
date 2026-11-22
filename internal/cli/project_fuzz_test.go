package cli

import (
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
		if strings.ContainsAny(id, "@ \t\n") || !strings.Contains(id, "/") || id != strings.ToLower(id) {
			t.Errorf("NormaliseRemote(%q) = %q", in, id)
		}
	})
}
