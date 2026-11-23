package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// #298: ValidateDir (goldie validate) and StartLocal (goldie server -dev)
// build the content-load error through one helper, so for the same broken
// content the two messages are equal. The helper is unexported and has no
// seam of its own; this pins what it guarantees.
func TestValidateDirAndStartLocalSayTheSameForBrokenContent(t *testing.T) {
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, "global", "articles"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "global", "articles", "broken.md"), []byte("no frontmatter here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"article without frontmatter": bad,
		"not a directory":             file,
		"missing":                     filepath.Join(t.TempDir(), "missing"),
	} {
		t.Run(name, func(t *testing.T) {
			vErr := ValidateDir(context.Background(), dir)
			if vErr == nil {
				t.Fatal("ValidateDir succeeded on broken content")
			}
			_, _, stop, sErr := StartLocal(context.Background(), dir)
			if sErr == nil {
				if stop != nil {
					stop()
				}
				t.Fatal("StartLocal succeeded on broken content")
			}
			if vErr.Error() != sErr.Error() {
				t.Errorf("ValidateDir says %q, StartLocal says %q: they must be the same", vErr, sErr)
			}
		})
	}
}
