package goldie_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests drive ./run itself, at its command line. A stand-in "go" (or
// oapi-codegen) that blocks or exits with a chosen status takes the place of
// the real tool, and TMPDIR points at an empty directory, so the temporary
// files a cmd_* function creates are visible and countable.

const (
	exitInterrupted = 130 // 128 + SIGINT, as cmd_ux already does
	exitTerminated  = 143 // 128 + SIGTERM
)

// blocking announces itself, then waits to be interrupted.
const blocking = "touch \"$READY_FILE\"\nexec sleep 60"

type runCase struct {
	name     string
	args     []string
	goScript string // body of the stand-in "go" on PATH
	toolStub string // body of the stand-in oapi-codegen under GOLDIE_TOOLS, if any
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // an executable stub
		t.Fatal(err)
	}
}

// startRun starts ./run with the stubs and returns the command, the empty
// TMPDIR it uses and a combined output buffer.
func startRun(t *testing.T, c runCase, ready string) (*exec.Cmd, string, *bytes.Buffer) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("./run is a POSIX shell script")
	}
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	tmp := filepath.Join(base, "tmp")
	tools := filepath.Join(base, "tools")
	if err := os.MkdirAll(tmp, 0o750); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(bin, "go"), c.goScript)
	if c.toolStub != "" {
		writeExec(t, filepath.Join(tools, "oapi-codegen-v2.9.0", "oapi-codegen"), c.toolStub)
	}
	cmd := exec.Command("./run", c.args...) //nolint:gosec // the repository's own script
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TMPDIR="+tmp,
		"GOLDIE_TOOLS="+tools,
		"READY_FILE="+ready,
	)
	// Its own process group, so a signal reaches the stand-in tool too, as a
	// terminal's Ctrl-C reaches the foreground group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd, tmp, out
}

func leftovers(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// exitCode is the exit status, or -1 when the process died of a signal.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -2
}

func TestRunRemovesItsTempFilesWhenInterrupted(t *testing.T) {
	cases := []runCase{
		{name: "test", args: []string{"test"}, goScript: blocking},
		{name: "regen-check", args: []string{"regen-check"}, goScript: blocking, toolStub: blocking},
	}
	signals := []struct {
		name string
		sig  syscall.Signal
		code int
	}{
		{"INT", syscall.SIGINT, exitInterrupted},
		{"TERM", syscall.SIGTERM, exitTerminated},
	}
	for _, c := range cases {
		for _, s := range signals {
			t.Run(c.name+"/"+s.name, func(t *testing.T) {
				ready := filepath.Join(t.TempDir(), "ready")
				cmd, tmp, out := startRun(t, c, ready)

				deadline := time.Now().Add(20 * time.Second)
				for {
					if _, err := os.Stat(ready); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("the wrapped command never started:\n%s", out.String())
					}
					time.Sleep(20 * time.Millisecond)
				}
				if leftovers(t, tmp) == 0 {
					t.Fatal("the command made no temporary file, so the test proves nothing")
				}

				if err := syscall.Kill(-cmd.Process.Pid, s.sig); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				var werr error
				select {
				case werr = <-done:
				case <-time.After(20 * time.Second):
					t.Fatal("./run did not exit after the signal")
				}

				if n := leftovers(t, tmp); n != 0 {
					t.Errorf("temporary files left behind after %s: %d", s.name, n)
				}
				if got := exitCode(werr); got != s.code {
					t.Errorf("exit status after %s = %d, want %d", s.name, got, s.code)
				}
				if strings.Contains(out.String(), tmp) {
					t.Errorf("the output names the temporary directory")
				}
			})
		}
	}
}

func TestRunKeepsTheWrappedCommandsStatusAndRemovesItsTempFile(t *testing.T) {
	cmd, tmp, out := startRun(t, runCase{
		name: "test", args: []string{"test"},
		goScript: "echo wrapped-output\nexit 7",
	}, filepath.Join(t.TempDir(), "unused"))
	if got := exitCode(cmd.Wait()); got != 7 {
		t.Errorf("exit status = %d, want the wrapped go test's 7\n%s", got, out.String())
	}
	if n := leftovers(t, tmp); n != 0 {
		t.Errorf("temporary files left behind: %d", n)
	}
	if !strings.Contains(out.String(), "wrapped-output") {
		t.Errorf("the wrapped command's output was not shown")
	}
	if strings.Contains(out.String(), tmp) {
		t.Errorf("the output names the temporary directory")
	}
}

func TestRunRemovesItsTempFilesWhenTheGeneratorFails(t *testing.T) {
	cmd, tmp, out := startRun(t, runCase{
		name: "regen-check", args: []string{"regen-check"},
		goScript: "exit 0", toolStub: "exit 7",
	}, filepath.Join(t.TempDir(), "unused"))
	if got := exitCode(cmd.Wait()); got == 0 {
		t.Errorf("regen-check passed although the generator failed\n%s", out.String())
	}
	if n := leftovers(t, tmp); n != 0 {
		t.Errorf("temporary files left behind: %d", n)
	}
	if strings.Contains(out.String(), tmp) {
		t.Errorf("the output names the temporary directory")
	}
}
