package engine

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// TestSSHDialerUnknownLoadFailsClosed resolves the dialer for a load that is
// not registered: there is no dialer, so go-git cannot fall back to an
// unbounded direct connection.
func TestSSHDialerUnknownLoadFailsClosed(t *testing.T) {
	useGitTransports()
	d, err := proxy.FromURL(&url.URL{Scheme: sshDialScheme, Host: "no-such-load"}, proxy.Direct)
	if err == nil || d != nil {
		t.Fatalf("FromURL = %v, %v; want an error", d, err)
	}
}

// TestSSHDialerForgetsItsLoad checks that a Load's dialer is gone once the
// Load is done.
func TestSSHDialerForgetsItsLoad(t *testing.T) {
	useGitTransports()
	opts, forget := sshProxy(&gitBudget{wire: 1, mem: 1}, time.Now().Add(time.Minute))
	u, err := url.Parse(opts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.FromURL(u, proxy.Direct); err != nil {
		t.Fatalf("registered load: %v", err)
	}
	forget()
	if _, err := proxy.FromURL(u, proxy.Direct); err == nil {
		t.Fatal("the load's dialer outlived the load")
	}
}

// TestSSHDialerRefusesItselfAsForward is ALL_PROXY naming a load dialer: the
// forward dialer is the sshDialer itself, which would recurse without end. The
// dial fails instead, and never goes out unbudgeted.
func TestSSHDialerRefusesItselfAsForward(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	d := &sshDialer{budget: &gitBudget{wire: 1 << 20, mem: 1 << 20}, deadline: time.Now().Add(time.Minute)}
	c, err := d.dialVia(context.Background(), d, "tcp", ln.Addr().String())
	if err == nil {
		_ = c.Close()
		t.Fatal("dialled through itself")
	}
}
