//go:build linux

package platform

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// openPTY returns a pseudo-terminal pair: logging to the slave is logging to a
// terminal, and the master shows what a person would read.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal available: %v", err)
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		_ = m.Close()
		t.Skipf("pseudo-terminal unlock: %v", e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		_ = m.Close()
		t.Skipf("pseudo-terminal name: %v", e)
	}
	name := "/dev/pts/" + strconv.FormatUint(uint64(n), 10)
	s, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = m.Close()
		t.Skipf("open %s: %v", name, err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = m.Close() })
	return m, s
}

func checkJSONLine(t *testing.T, got []byte) {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(got), &rec); err != nil {
		t.Fatalf("not one JSON object: %q: %v", got, err)
	}
	for _, k := range []string{"ts", "level", "msg", "component", "version"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing key %q in %v", k, rec)
		}
	}
	if rec["level"] != "info" || rec["msg"] != "hello" || rec["component"] != "server" {
		t.Errorf("unexpected values: %v", rec)
	}
}

func TestNewLoggerWritesJSONWhenTheWriterIsNotATerminal(t *testing.T) {
	t.Run("buffer", func(t *testing.T) {
		var buf bytes.Buffer
		NewLogger(&buf, slog.LevelInfo, "server").Info("hello")
		checkJSONLine(t, buf.Bytes())
	})
	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "log")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		NewLogger(f, slog.LevelInfo, "server").Info("hello")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		checkJSONLine(t, got)
	})
}

func TestNewLoggerWritesTextWhenTheWriterIsATerminal(t *testing.T) {
	master, slave := openPTY(t)

	NewLogger(slave, slog.LevelInfo, "server").Info("hello", "k", "v")

	_ = master.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	n, err := master.Read(buf)
	if err != nil {
		t.Fatalf("read terminal output: %v", err)
	}
	line := strings.TrimSpace(string(buf[:n]))
	if strings.HasPrefix(line, "{") {
		t.Fatalf("terminal got JSON, want text: %q", line)
	}
	for _, want := range []string{"ts=", "level=info", "msg=hello", "component=server", "version=", "k=v"} {
		if !strings.Contains(line, want) {
			t.Errorf("text line lacks %q: %q", want, line)
		}
	}
	if strings.Contains(line, "time=") || strings.Contains(line, "level=INFO") {
		t.Errorf("text keys differ from the JSON keys: %q", line)
	}
}
