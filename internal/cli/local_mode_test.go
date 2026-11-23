package cli_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/platform"
)

// Acceptance tests for #306: in local mode a connection failure is about the
// embedded server, not about --addr or GOLDIE_ADDR, and local mode without a
// StartLocal says so. Remote mode keeps its wording.

const localTestToken = "local-mode-test-token-0123456789abcdef0123456789abcdef"

func localEnv(vars map[string]string, out *bytes.Buffer) cli.Env {
	return cli.Env{
		Lookup: func(k string) (string, bool) {
			v, ok := vars[k]
			return v, ok
		},
		ReadFile: os.ReadFile,
		Out:      out,
	}
}

// closedAddr returns a loopback URL nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "http://" + addr
}

// An embedded server that cannot be reached is reported as such, without the
// advice about --addr and GOLDIE_ADDR, which do not apply in local mode.
func TestLocalModeConnectionErrorNamesTheEmbeddedServer(t *testing.T) {
	cases := []struct {
		name string
		args []string
		vars map[string]string
	}{
		{"search --dir", []string{"search", "kubernetes", "--dir", "/content"}, nil},
		{"lookup --dir", []string{"lookup", "platform/deploy", "--dir", "/content"}, nil},
		{"search GOLDIE_DIR", []string{"search", "kubernetes"}, map[string]string{platform.EnvDir: "/content"}},
		{"lookup GOLDIE_DIR", []string{"lookup", "platform/deploy"}, map[string]string{platform.EnvDir: "/content"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			env := localEnv(c.vars, &out)
			dead := closedAddr(t)
			env.StartLocal = func(ctx context.Context, dir string) (string, string, func(), error) {
				return dead, localTestToken, func() {}, nil
			}
			err := cli.Run(context.Background(), c.args, env)
			if err == nil {
				t.Fatalf("no error for an unreachable embedded server; output %q", out.String())
			}
			msg := err.Error()
			for _, bad := range []string{"--addr", "GOLDIE_ADDR"} {
				if strings.Contains(msg, bad) {
					t.Errorf("local-mode error mentions %s: %q", bad, msg)
				}
			}
			low := strings.ToLower(msg)
			if !strings.Contains(low, "embedded") && !strings.Contains(low, "local") {
				t.Errorf("error does not name the embedded or local server: %q", msg)
			}
			if !strings.Contains(low, "reach") {
				t.Errorf("error does not say the server could not be reached: %q", msg)
			}
			if strings.Contains(msg, dead) || strings.Contains(msg, localTestToken) {
				t.Errorf("error carries the address or token: %q", msg)
			}
		})
	}
}

// Remote mode is unchanged: it still points at --addr and GOLDIE_ADDR.
func TestRemoteModeConnectionErrorKeepsAddrAdvice(t *testing.T) {
	var out bytes.Buffer
	env := localEnv(map[string]string{platform.EnvToken: localTestToken}, &out)
	env.StartLocal = func(context.Context, string) (string, string, func(), error) {
		t.Error("StartLocal called in remote mode")
		return "", "", nil, errors.New("unexpected")
	}
	err := cli.Run(context.Background(), []string{"search", "x", "--addr", closedAddr(t)}, env)
	if err == nil {
		t.Fatal("no error for an unreachable server")
	}
	for _, want := range []string{"--addr", "GOLDIE_ADDR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("remote error lacks %s: %q", want, err)
		}
	}
}

// Without a StartLocal, --dir and GOLDIE_DIR fail with "local mode is not
// available", through Run and through Open, and never start anything.
func TestLocalModeWithoutStartLocalIsNotAvailable(t *testing.T) {
	t.Run("run --dir", func(t *testing.T) {
		var out bytes.Buffer
		err := cli.Run(context.Background(), []string{"search", "x", "--dir", "/content"}, localEnv(nil, &out))
		if err == nil || !strings.Contains(err.Error(), "local mode is not available") {
			t.Fatalf("err = %v, want it to say local mode is not available", err)
		}
		if out.Len() != 0 {
			t.Errorf("output on failure: %q", out.String())
		}
	})
	t.Run("run GOLDIE_DIR", func(t *testing.T) {
		var out bytes.Buffer
		env := localEnv(map[string]string{platform.EnvDir: "/content"}, &out)
		err := cli.Run(context.Background(), []string{"status"}, env)
		if err == nil || !strings.Contains(err.Error(), "local mode is not available") {
			t.Fatalf("err = %v, want it to say local mode is not available", err)
		}
	})
	t.Run("open", func(t *testing.T) {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		f := cli.BindClientFlags(fs)
		if err := fs.Parse([]string{"--dir", "/content"}); err != nil {
			t.Fatal(err)
		}
		addr, token, stop, err := f.Open(context.Background(), localEnv(nil, &bytes.Buffer{}))
		if err == nil || !strings.Contains(err.Error(), "local mode is not available") {
			t.Fatalf("err = %v, want it to say local mode is not available", err)
		}
		if addr != "" || token != "" || stop != nil {
			t.Errorf("Open returned addr %q token %q stop-nil=%v with an error", addr, token, stop == nil)
		}
	})
	t.Run("no dir is not affected", func(t *testing.T) {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		f := cli.BindClientFlags(fs)
		if err := fs.Parse([]string{"--addr", "http://127.0.0.1:1", "--token", localTestToken}); err != nil {
			t.Fatal(err)
		}
		_, _, stop, err := f.Open(context.Background(), localEnv(nil, &bytes.Buffer{}))
		if err != nil {
			t.Fatalf("remote Open with a nil StartLocal failed: %v", err)
		}
		stop()
	})
}
