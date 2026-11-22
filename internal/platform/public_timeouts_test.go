package platform

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// shortPublicTimeouts sets the three timeouts under test. Tests keep the
// others long, so a cut-off can only come from the timeout under test.
//
// Hook contract (issue #170): server.go declares `appReadTimeout` as a
// time.Duration var in the same var block as readHeaderTimeout, idleTimeout
// and adminIOTimeout. It is wired to the public http.Server's ReadTimeout, and
// these tests shorten it. Until it exists the package does not compile.
func shortPublicTimeouts(t *testing.T, header, read, idle time.Duration) {
	t.Helper()
	oh, or, oi := readHeaderTimeout, appReadTimeout, idleTimeout
	readHeaderTimeout, appReadTimeout, idleTimeout = header, read, idle
	t.Cleanup(func() { readHeaderTimeout, appReadTimeout, idleTimeout = oh, or, oi })
}

// closedByServer reports whether the server ends the connection before the
// deadline, whether or not it first writes an error response.
func closedByServer(conn net.Conn, within time.Duration) bool {
	_ = conn.SetReadDeadline(time.Now().Add(within))
	_, err := io.Copy(io.Discard, conn)
	if err == nil {
		return true // EOF
	}
	var ne net.Error
	return !(errors.As(err, &ne) && ne.Timeout()) && !errors.Is(err, os.ErrDeadlineExceeded)
}

func TestPublicListenerCutsOffSlowHeaders(t *testing.T) {
	shortPublicTimeouts(t, 200*time.Millisecond, time.Minute, time.Minute)
	s := start(t, 0, nil)
	conn, err := net.Dial("tcp", s.app)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Headers never finish: no blank line.
	if _, err := io.WriteString(conn, "GET /x HTTP/1.1\r\nHost: x\r\nX-Slow: "); err != nil {
		t.Fatal(err)
	}
	if !closedByServer(conn, 3*time.Second) {
		t.Fatal("a client sending headers slowly was not cut off after ReadHeaderTimeout")
	}
}

func TestPublicListenerCutsOffSlowBody(t *testing.T) {
	shortPublicTimeouts(t, time.Minute, 300*time.Millisecond, time.Minute)
	bodyErr := make(chan error, 1)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		select {
		case bodyErr <- err:
		default:
		}
		w.WriteHeader(http.StatusBadRequest)
	})
	s := start(t, 0, h)
	conn, err := net.Dial("tcp", s.app)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Promises 100 body bytes, sends 3, then stalls.
	if _, err := io.WriteString(conn, "POST /x HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\nabc"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-bodyErr:
		if err == nil {
			t.Fatal("handler read a complete body that the client never sent")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a client stalling mid-body was not cut off: the public server has no ReadTimeout")
	}
	if !closedByServer(conn, 3*time.Second) {
		t.Fatal("connection stayed open after the read timeout")
	}
}

func TestPublicListenerStillServesFastRequestsUnderShortTimeouts(t *testing.T) {
	shortPublicTimeouts(t, 2*time.Second, 2*time.Second, 2*time.Second)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(b)
	})
	s := start(t, 0, h)
	resp, err := http.Post("http://"+s.app+"/x", "text/plain", strings.NewReader("hello")) //nolint:gosec // loopback test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "hello" {
		t.Fatalf("got %d %q, want 200 hello", resp.StatusCode, b)
	}
}

func TestPublicListenerClosesIdleKeepAlive(t *testing.T) {
	shortPublicTimeouts(t, time.Minute, time.Minute, 200*time.Millisecond)
	s := start(t, 0, nil)
	conn, err := net.Dial("tcp", s.app)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "GET /nothing HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("idle keep-alive connection not closed after IdleTimeout: %v", err)
	}
}

// No server-wide WriteTimeout: a response that takes longer than every other
// timeout, written in slow chunks, still arrives whole.
func TestPublicListenerLongResponsesAreNotCutOff(t *testing.T) {
	shortPublicTimeouts(t, 150*time.Millisecond, 150*time.Millisecond, 150*time.Millisecond)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fl, _ := w.(http.Flusher)
		for _, part := range []string{"one ", "two ", "three"} {
			time.Sleep(300 * time.Millisecond)
			_, _ = io.WriteString(w, part)
			if fl != nil {
				fl.Flush()
			}
		}
	})
	s := start(t, 0, h)
	resp, err := http.Get("http://" + s.app + "/slow") //nolint:gosec // loopback test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil || string(b) != "one two three" {
		t.Fatalf("slow response = %q, err %v; want it delivered whole (no WriteTimeout)", b, err)
	}
}
