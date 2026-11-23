package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #485: an unreadable --dir says what to do next, and never prints the path.
const (
	unreadableDirMsg  = "content directory is not readable"
	unreadableDirHint = "check that the content directory exists and is readable"
)

func TestSearchUnreadableDirNamesANextStep(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "s3cret-a-file")
	if err := os.WriteFile(file, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{
		"missing directory":             filepath.Join(base, "s3cret-no-such-dir"),
		"a file instead of a directory": file,
	}
	for name, dir := range dirs {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "search", "foo", "--dir", dir)
			if code == 0 {
				t.Errorf("exit code = 0, want non-zero\nstderr:\n%s", stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, unreadableDirMsg) {
				t.Errorf("stderr = %q, want %q", stderr, unreadableDirMsg)
			}
			if !strings.Contains(stderr, unreadableDirHint) {
				t.Errorf("stderr = %q, want the hint %q", stderr, unreadableDirHint)
			}
			for _, p := range []string{dir, base, "s3cret", os.TempDir()} {
				if strings.Contains(stdout+stderr, p) {
					t.Errorf("output echoes the path %q:\n%s", p, stderr)
				}
			}
		})
	}
}

func TestValidateUnreadableDirNamesTheSameNextStep(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "s3cret-no-such-dir")
	stdout, stderr, code := runValidate(t, t.TempDir(), validateEnv(), "validate", missing)
	reason := wantInvalid(t, stdout, stderr, code, missing, "s3cret", base)
	if !strings.Contains(reason, unreadableDirMsg) || !strings.Contains(reason, unreadableDirHint) {
		t.Errorf("reason = %q, want %q and the hint %q", reason, unreadableDirMsg, unreadableDirHint)
	}
}
