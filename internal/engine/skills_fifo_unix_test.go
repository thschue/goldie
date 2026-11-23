//go:build unix

package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/engine"
)

// Issue #406: a FIFO in a skill directory never blocks Load and is never
// served as a file of the bundle. Load refuses it or loads without it.
func TestSkillDoesNotBlockOnAFIFO(t *testing.T) {
	for name, place := range map[string]string{
		"in the bundle root":  "global/skills/x/pipe",
		"in a subdirectory":   "global/skills/x/refs/pipe",
		"named SKILL.md":      "global/skills/pipe/SKILL.md",
		"named like markdown": "global/skills/x/pipe.md",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := writeFiles(t, map[string]string{
				globalSkillFile("x", "SKILL.md"): skillDoc("x", "d"),
				globalSkillFile("x", "kept.md"):  "kept",
			})
			fifo := filepath.Join(root, filepath.FromSlash(place))
			if err := os.MkdirAll(filepath.Dir(fifo), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(fifo, 0o644); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
			type result struct {
				snap *engine.Snapshot
				err  error
			}
			done := make(chan result, 1)
			go func() {
				snap, err := engine.NewDirSource(root).Load(t.Context())
				done <- result{snap, err}
			}()
			select {
			case r := <-done:
				if r.err != nil {
					if strings.Contains(r.err.Error(), root) {
						t.Errorf("error %q leaks the root path", r.err)
					}
					return
				}
				for _, n := range []string{"x", "pipe"} {
					s, ok := r.snap.SkillIn("", n)
					if !ok {
						continue
					}
					for _, f := range s.Files {
						if f.Path == "pipe" || f.Path == "refs/pipe" || f.Path == "pipe.md" || (n == "pipe" && f.Path == "SKILL.md") {
							t.Errorf("skill %s serves the FIFO as %s", n, f.Path)
						}
					}
				}
			case <-time.After(fifoLoadBound):
				t.Fatal("Load blocked on a FIFO in a skill directory")
			}
		})
	}
}

var errFIFOServed = errors.New("the FIFO was served as a file of the bundle")

func TestLocalSkillDoesNotBlockOnAFIFO(t *testing.T) {
	src, _, ok := localFSSource(t, func(t *testing.T, repo string) error {
		writeTo(t, repo, localSkillFile("mine", "SKILL.md"), skillDoc("mine", "Mine"))
		return syscall.Mkfifo(filepath.Join(repo, ".goldie", "skills", "mine", "pipe"), 0o644)
	})
	if !ok {
		t.Skip("mkfifo unavailable")
	}
	done := make(chan error, 1)
	go func() {
		snap, err := src.Load(t.Context())
		if err == nil {
			if s, ok := snap.SkillIn("", "mine"); ok {
				for _, f := range s.Files {
					if f.Path == "pipe" {
						err = errFIFOServed
					}
				}
			}
		}
		done <- err
	}()
	select {
	case err := <-done:
		if errors.Is(err, errFIFOServed) {
			t.Error(err)
		}
	case <-time.After(fifoLoadBound):
		t.Fatal("Load blocked on a FIFO in a local skill directory")
	}
}
