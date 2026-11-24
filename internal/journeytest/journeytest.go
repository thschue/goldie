// Package journeytest starts the built goldie binary as `server --dev` on
// examples/knowledge, for the MVP journey tests (J4 in internal/mcp, J5 in
// internal/cli). It is for tests only and imports nothing from goldie.
package journeytest

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// exportRE is the whole export line of `goldie server --dev`; group 1 is the
// address, group 2 the token.
var exportRE = regexp.MustCompile(`^export GOLDIE_ADDR=(http://127\.0\.0\.1:[1-9][0-9]{0,4}) GOLDIE_TOKEN=([0-9a-f]{64})$`)

// Server builds goldie from cmd/goldie, starts `server --dev` on
// examples/knowledge on a free port and returns the binary path, the server's
// address and its token. The server stops when the test ends. The caller's
// working directory must be a package directory two levels below the module
// root (internal/<module>), as the journey tests are. The build and the
// server make it slow, so the journeys skip under go test -short.
func Server(t *testing.T) (bin, addr, token string) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds goldie and starts a server")
	}
	bin = filepath.Join(t.TempDir(), "goldie")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/goldie").CombinedOutput(); err != nil {
		t.Fatalf("build goldie: %v\n%s", err, out)
	}
	content, err := filepath.Abs("../../examples/knowledge")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "server", "--dev", content)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GOLDIE_DRAIN_SECONDS=0"}
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	found := make(chan []string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if m := exportRE.FindStringSubmatch(sc.Text()); m != nil {
				found <- m
				break
			}
		}
		_ = sc.Err()
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case m := <-found:
		return bin, m[1], m[2]
	case <-time.After(30 * time.Second):
		t.Fatal("goldie server --dev did not print its export line")
		return "", "", ""
	}
}
