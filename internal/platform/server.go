package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// ErrForced is returned by Serve when a second stop signal cut shutdown
// short.
var ErrForced = errors.New("forced shutdown")

// shutdownGrace bounds how long in-flight requests may finish after the
// drain period (PC-08).
const shutdownGrace = 30 * time.Second

// Connection timeouts. Idle keep-alive connections are closed on both ports.
// Probes are short, so the admin port also bounds each whole request and
// response. The app port bounds reading a whole request (headers and body)
// with appReadTimeout but sets no WriteTimeout: responses can be long, and
// /mcp bounds its own requests. Variables so tests can shorten them.
var (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 60 * time.Second
	adminIOTimeout    = 10 * time.Second
	appReadTimeout    = 30 * time.Second
)

// newConns tracks connections that have not sent a request yet. http.Server
// Shutdown waits up to five seconds for such a connection, so Serve closes
// them itself when shutdown begins.
type newConns struct {
	mu sync.Mutex
	m  map[net.Conn]struct{}
}

// track is the http.Server ConnState hook.
func (n *newConns) track(c net.Conn, st http.ConnState) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if st == http.StateNew {
		if n.m == nil {
			n.m = map[net.Conn]struct{}{}
		}
		n.m[c] = struct{}{}
		return
	}
	delete(n.m, c)
}

func (n *newConns) closeAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for c := range n.m {
		_ = c.Close()
	}
	n.m = nil
}

// Server is the process skeleton: the app listener, the admin listener and
// the drain sequence. Role handlers plug in through App.
type Server struct {
	Config Config
	Logger *slog.Logger
	Ready  *Registry
	// App serves the public listener. nil means every path is the not-found
	// envelope. Probes and /version are admin-only (PC-01).
	App http.Handler
	// Telemetry traces the public listener and is flushed in the drain. nil
	// means no telemetry.
	Telemetry *Telemetry
	// OnListen, when set, is called with each bound address ("app" or
	// "admin") before serving starts. Tests use it with port 0.
	OnListen func(listener, addr string)
}

// listenError names the variable and keeps only the system error (such as
// "bind: address already in use"); the address itself is not echoed (PC-05).
func listenError(name string, err error) error {
	var se *os.SyscallError
	if errors.As(err, &se) {
		return fmt.Errorf("%s: cannot listen: %w", name, se)
	}
	return fmt.Errorf("%s: cannot listen on this address", name)
}

func versionHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, CurrentVersion())
}

// notFoundHint is static on purpose: it echoes nothing from the request, and
// platform cannot import contract to share the API's hint wording.
const notFoundHint = "the admin port serves /livez, /readyz and /version; the public listener serves the API and the UI"

func notFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{
		"code":    "not_found",
		"message": "no such endpoint",
		"details": map[string]any{"hint": notFoundHint},
	}})
}

func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.Ready.handler)
	mux.HandleFunc("GET /version", versionHandler)
	mux.HandleFunc("/", notFound)
	return mux
}

func (s *Server) appHandler() http.Handler {
	mux := http.NewServeMux()
	if s.App != nil {
		mux.Handle("/", s.App)
	} else {
		mux.HandleFunc("/", notFound)
	}
	return mux
}

// Serve listens on both ports and blocks until ctx is cancelled (the first
// stop signal). It then fails readiness, keeps serving for the drain period,
// and shuts both listeners down gracefully, returning nil (PC-08). Closing
// force (the second stop signal) aborts at once with ErrForced.
func (s *Server) Serve(ctx context.Context, force <-chan struct{}) error {
	if s.Ready == nil {
		s.Ready = NewRegistry(0)
	}
	appLn, err := net.Listen("tcp", s.Config.Addr)
	if err != nil {
		return listenError(EnvAddr, err)
	}
	adminLn, err := net.Listen("tcp", s.Config.AdminAddr)
	if err != nil {
		_ = appLn.Close()
		return listenError(EnvAdminAddr, err)
	}
	var appNew, adminNew newConns
	app := &http.Server{
		ConnState:         appNew.track,
		Handler:           s.Telemetry.WrapHandler(s.appHandler()),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       appReadTimeout,
		IdleTimeout:       idleTimeout,
	}
	admin := &http.Server{
		ConnState:         adminNew.track,
		Handler:           s.adminHandler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       adminIOTimeout,
		WriteTimeout:      adminIOTimeout,
		IdleTimeout:       idleTimeout,
	}
	closeAll := func() {
		_ = app.Close()
		_ = admin.Close()
	}

	for name, ln := range map[string]net.Listener{"app": appLn, "admin": adminLn} {
		s.Logger.Info("listening", "listener", name, "addr", ln.Addr().String())
		if s.OnListen != nil {
			s.OnListen(name, ln.Addr().String())
		}
	}

	errc := make(chan error, 2)
	go func() { errc <- app.Serve(appLn) }()
	go func() { errc <- admin.Serve(adminLn) }()

	select {
	case err := <-errc:
		closeAll()
		return fmt.Errorf("listener failed: %w", err)
	case <-ctx.Done():
	}

	s.Ready.Drain()
	s.Logger.Info("draining", "seconds", int(s.Config.DrainTimeout/time.Second))
	timer := time.NewTimer(s.Config.DrainTimeout)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-force:
		closeAll()
		return ErrForced
	}

	s.Logger.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	forced := make(chan struct{})
	go func() {
		select {
		case <-force:
			close(forced)
			cancel()
		case <-sctx.Done():
		}
	}()
	// The app listener drains first; the admin port stays up until it is done
	// so probes keep reporting "draining" rather than a refused connection.
	// Connections that never sent a request hold nothing in flight; close them
	// so Shutdown does not wait for them.
	appNew.closeAll()
	adminNew.closeAll()
	err = errors.Join(app.Shutdown(sctx), admin.Shutdown(sctx))
	if err != nil {
		closeAll()
		select {
		case <-forced:
			return ErrForced
		default:
		}
		return fmt.Errorf("shutdown: %w", err)
	}
	// Flush spans and log records once no request is left to add more.
	if err := s.Telemetry.Shutdown(sctx); err != nil {
		s.Logger.Warn("telemetry flush failed")
	}
	s.Logger.Info("stopped")
	return nil
}
