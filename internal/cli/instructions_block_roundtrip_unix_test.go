//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/thschue/goldie/internal/cli"
)

// A new file gets 0644 less the umask, so a restrictive umask is honoured and
// the temp file's own 0600 never leaks into the result.
func TestApplyBlockFileNewFileHonoursTheUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	dir := t.TempDir()
	if _, err := cli.ApplyBlockFile(dir, "CLAUDE.md", blockFixture(t, "content.md")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v; want 0600 under umask 077", fi.Mode().Perm(), err)
	}
}
