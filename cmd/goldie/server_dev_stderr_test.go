package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// #293: with --dev, log records go to stderr and stdout carries exactly one
// line, the export line, so eval "$(goldie server --dev)" works and anything
// capturing stdout does not capture logs. Without --dev, logs stay on stdout
// (PC-07).

// assertDevLogsOnStderr checks the stream split of a finished --dev run: stdout
// is exactly the export line, and stderr holds the log records, each a JSON
// object with a component, including the startup report and "listening".
func assertDevLogsOnStderr(t *testing.T, p *devProc) {
	t.Helper()
	out, errs := p.lines()
	if len(out) != 1 || !devExportRE.MatchString(out[0]) {
		t.Errorf("stdout = %q, want exactly the export line\n%s", out, p.dump())
	}
	for _, l := range out {
		if json.Valid([]byte(l)) {
			t.Errorf("a log record is on stdout: %q", l)
		}
	}
	seen := map[string]bool{}
	for _, l := range errs {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Errorf("stderr line is not a JSON log record: %q", l)
			continue
		}
		if _, ok := m["component"]; !ok {
			t.Errorf("stderr record lacks component: %s", l)
		}
		msg, _ := m["msg"].(string)
		seen[msg] = true
	}
	for _, want := range []string{"listening", "config"} {
		if !seen[want] {
			t.Errorf("stderr has no %q log record\n%s", want, p.dump())
		}
	}
}

// Stdout is the export line alone, from start through requests at debug level
// to shutdown; the logs, including the startup report and the "listening"
// lines, are on stderr; the token is not on stderr.
func TestServerDevStdoutIsOnlyTheExportLine(t *testing.T) {
	p := startDev(t, devEnv("GOLDIE_LOG_LEVEL=debug"), contentDir(t))
	if got := devStatus(t, p.addr, devArticle, p.token); got != http.StatusOK {
		t.Errorf("GET with the printed token = %d, want 200", got)
	}
	devStatus(t, p.addr, devArticle, "")
	devStatus(t, p.addr, "/mcp", p.token)
	p.stop(t)
	assertDevLogsOnStderr(t, p)
	assertTokenOnlyInExportLine(t, p)
}

// The one line saying --dev ignores the address and token variables is a log
// record too: it goes to stderr, not stdout.
func TestServerDevIgnoredVariablesLineGoesToStderr(t *testing.T) {
	p := startDev(t, devEnv("GOLDIE_ADDR=0.0.0.0:0", "GOLDIE_API_TOKEN=env-token-0123456789abcdef0123456789abcdef"), contentDir(t))
	p.stop(t)
	assertDevLogsOnStderr(t, p)
	out, errs := p.lines()
	for _, l := range out {
		if strings.Contains(l, devIgnoredMsg) {
			t.Errorf("the ignored-variables line is on stdout: %q", l)
		}
	}
	found := false
	for _, l := range errs {
		found = found || strings.Contains(l, devIgnoredMsg)
	}
	if !found {
		t.Errorf("the ignored-variables line is not on stderr\n%s", p.dump())
	}
}

// A shell that evals goldie's stdout gets GOLDIE_ADDR and GOLDIE_TOKEN set,
// the eval reports no error (log records would be run as commands) and stdout
// is one line. The server keeps running, so the script captures
// stdout in a file (what $(...) would hand to eval once the command ends),
// evals the whole file, prints both variables, and stops the server.
func TestServerDevStdoutEvalsInAShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	script := `out=$(mktemp) || exit 2
"$1" server --dev "$2" >"$out" 2>/dev/null &
pid=$!
i=0
until grep -q '^export GOLDIE_ADDR=' "$out" 2>/dev/null; do
  i=$((i+1)); [ "$i" -gt 100 ] && { kill "$pid"; exit 3; }
  sleep 0.1
done
sleep 0.3
eval "$(cat "$out")" 2>"$out.err" || { kill "$pid"; exit 4; }
[ ! -s "$out.err" ] || { kill "$pid"; cat "$out.err" >&2; exit 5; }
[ "$(wc -l <"$out")" -eq 1 ] || { kill "$pid"; exit 6; }
kill "$pid"; wait "$pid" 2>/dev/null
rm -f "$out" "$out.err"
printf '%s\n%s\n' "$GOLDIE_ADDR" "$GOLDIE_TOKEN"`
	cmd := exec.Command(sh, "-c", script, "sh", goldieBinary(t), contentDir(t))
	cmd.Env = append(devEnv(), "TMPDIR="+t.TempDir())
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("eval of goldie server --dev stdout failed: %v\noutput: %s", err, b)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("shell printed %q, want the address and the token", b)
	}
	if m := devExportRE.FindStringSubmatch("export GOLDIE_ADDR=" + lines[0] + " GOLDIE_TOKEN=" + lines[1]); m == nil {
		t.Errorf("the shell got GOLDIE_ADDR=%q GOLDIE_TOKEN=%q, want a loopback URL and 64 hex", lines[0], lines[1])
	}
}

// Without --dev nothing moves: every log record is on stdout (PC-07) and
// stderr stays empty.
func TestServerWithoutDevLogsStayOnStdout(t *testing.T) {
	env := devEnv(
		"GOLDIE_ADDR=127.0.0.1:0",
		"GOLDIE_ADMIN_ADDR=127.0.0.1:0",
		"GOLDIE_CONTENT_DIR="+contentDir(t),
		"GOLDIE_API_TOKEN_FILE="+tokenFile(t, testToken),
	)
	p := launchDev(t, "", env, "server")
	for {
		p.scan()
		if p.app != "" && p.admin != "" {
			break
		}
		select {
		case <-p.changed:
		case err := <-p.exited:
			t.Fatalf("goldie server exited early: %v\n%s", err, p.dump())
		}
	}
	p.stop(t)
	out, errs := p.lines()
	if len(errs) != 0 {
		t.Errorf("stderr = %q, want empty without --dev", errs)
	}
	seen := map[string]bool{}
	for _, l := range out {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Errorf("stdout line is not a JSON log record: %q", l)
			continue
		}
		msg, _ := m["msg"].(string)
		seen[msg] = true
	}
	for _, want := range []string{"listening", "config"} {
		if !seen[want] {
			t.Errorf("stdout has no %q log record\n%s", want, p.dump())
		}
	}
}
