package engine

import (
	"context"
	"net"
	"os"
	"os/exec"
	"runtime/debug"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// TestSSHDialerRefusesItselfBehindPerHost is the forward dialer wrapping the
// sshDialer, the way x/net's FromEnvironmentUsing does when NO_PROXY is set:
// a type check on the forward dialer misses it, and the dial must still fail
// rather than come back into the same dialer.
func TestSSHDialerRefusesItselfBehindPerHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	d := &sshDialer{budget: &gitBudget{wire: 1 << 20, mem: 1 << 20}, deadline: time.Now().Add(time.Minute)}
	c, err := d.dialVia(context.Background(), proxy.NewPerHost(d, proxy.Direct), "tcp", ln.Addr().String())
	if err == nil {
		_ = c.Close()
		t.Fatal("dialled through itself behind a PerHost")
	}
}

// TestSSHDialerALLProxyWithNOProxyDoesNotRecurse drives the real environment
// path in a child process, because x/net reads ALL_PROXY and NO_PROXY once
// per process: ALL_PROXY names the load's own dialer and NO_PROXY is set. The
// dial must fail; it must not recurse until the stack overflows.
func TestSSHDialerALLProxyWithNOProxyDoesNotRecurse(t *testing.T) {
	if os.Getenv("GOLDIE_SSH_REENTRY_CHILD") == "1" {
		sshReentryChild()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSSHDialerALLProxyWithNOProxyDoesNotRecurse$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOLDIE_SSH_REENTRY_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%.2000s", err, out)
	}
}

func sshReentryChild() {
	debug.SetMaxStack(64 << 20) // overflow fast instead of at 1 GB
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer func() { _ = ln.Close() }()
	useGitTransports()
	opts, forget := sshProxy(&gitBudget{wire: 1 << 20, mem: 1 << 20}, time.Now().Add(time.Minute))
	defer forget()
	if err := os.Setenv("ALL_PROXY", opts.URL); err != nil {
		panic(err)
	}
	if err := os.Setenv("NO_PROXY", "git.example.invalid"); err != nil {
		panic(err)
	}
	d, ok := sshLoads.Load(opts.URL[len(sshDialScheme+"://"):])
	if !ok {
		panic("load not registered")
	}
	c, err := d.(*sshDialer).DialContext(context.Background(), "tcp", ln.Addr().String())
	if err == nil {
		_ = c.Close()
		panic("dialled with ALL_PROXY naming the load's own dialer")
	}
}
