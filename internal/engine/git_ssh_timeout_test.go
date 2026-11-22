package engine_test

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/engine"
)

// TestGitSourceSSHStalledHandshakeTimesOut points a load at a server that
// accepts the TCP connection and never says a word, with the bound shortened:
// the load must fail with the fixed timeout error once the bound passes, not
// hang in the SSH handshake.
func TestGitSourceSSHStalledHandshakeTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
		wg    sync.WaitGroup
	)
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	})
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	kh := "[" + host + "]:" + port + " " + authorizedKey(newSSHKey(t).pub)
	src := newGit(t, engine.GitOptions{URL: "ssh://git@" + ln.Addr().String() + "/org/repo.git", SSHKey: newSSHKey(t).pem, KnownHosts: kh})
	engine.SetGitLoadTimeout(src, time.Second)

	done := make(chan error, 1)
	go func() { _, err := src.Load(t.Context()); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("err = %v, want the fixed timeout error", err)
		}
		assertFixedGitError(t, err)
		assertNoSecret(t, err.Error(), ln.Addr().String())
	case <-time.After(10 * time.Second):
		t.Fatal("a stalled SSH handshake held the load past its bound")
	}
}
