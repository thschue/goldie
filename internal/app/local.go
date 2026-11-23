package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"slices"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/platform"
)

// localAddr is where both listeners of a local server bind: loopback only, on
// a port the system picks.
const localAddr = "127.0.0.1:0"

// localTokenBytes is the size of a local server's random token, before hex.
const localTokenBytes = 32

// DevIgnoredMessage is the line goldie server -dev logs once when the
// environment sets a variable a local server makes itself.
const DevIgnoredMessage = "dev mode ignores GOLDIE_ADDR, GOLDIE_ADMIN_ADDR, GOLDIE_API_TOKEN and GOLDIE_API_TOKENS from the environment"

// devIgnored are the variables a local server never reads, each also in its
// _FILE form: it picks its own addresses and makes its own token.
var devIgnored = []string{
	platform.EnvAddr, platform.EnvAddr + "_FILE",
	platform.EnvAdminAddr, platform.EnvAdminAddr + "_FILE",
	platform.EnvAPIToken, platform.EnvAPIToken + "_FILE",
	platform.EnvAPITokens, platform.EnvAPITokens + "_FILE",
}

// DevLookup wraps lookup so that the variables a local server sets itself
// read as unset, so a bad or conflicting value there cannot stop it. ignored
// reports whether lookup had any of them, even empty.
func DevLookup(lookup platform.Lookup) (masked platform.Lookup, ignored bool) {
	ignored = slices.ContainsFunc(devIgnored, func(name string) bool {
		_, ok := lookup(name)
		return ok
	})
	return func(name string) (string, bool) {
		if slices.Contains(devIgnored, name) {
			return "", false
		}
		return lookup(name)
	}, ignored
}

// LocalConfig is base for a local server over the content at dir: both
// listeners on loopback with system-picked ports, and no token settings,
// because Local makes its own token. Everything else comes from base.
func LocalConfig(base platform.Config, dir string) platform.Config {
	base.Addr, base.AdminAddr = localAddr, localAddr
	base.APIToken, base.APITokens = "", ""
	base.ContentDir = dir
	return base
}

// Local runs the server on loopback over the content at Config.ContentDir with
// a fresh random token: what goldie server -dev and StartLocal run.
type Local struct {
	Config    platform.Config     // Start applies LocalConfig to it
	Logger    *slog.Logger        // nil discards
	Telemetry *platform.Telemetry // nil for none
	Force     <-chan struct{}     // closing it cuts the drain short; nil never does
}

// Start loads the content, makes the token and starts both listeners and the
// poll loop. It returns once the listeners are bound, with the public base URL
// (http://127.0.0.1:<port>) and the token, which it never logs. wait blocks
// until the listeners and the poll loop have stopped, which follows the end of
// ctx, and returns the server's result; it is safe to call more than once and
// concurrently. Content that does not load is an error, "content does not
// load: " and the loader's reason, without the directory.
func (l Local) Start(ctx context.Context) (addr, token string, wait func() error, err error) {
	if err := ctx.Err(); err != nil {
		return "", "", nil, err
	}
	cfg := LocalConfig(l.Config, l.Config.ContentDir) // loopback whatever the caller passed
	if err := checkRateLimit(cfg.RateLimit); err != nil {
		return "", "", nil, err
	}
	syncer, err := loadDir(ctx, cfg.ContentDir)
	if err != nil {
		return "", "", nil, contentLoadError(err)
	}
	token, err = newLocalToken()
	if err != nil {
		return "", "", nil, err
	}
	tokens, err := api.OperatorToken(token)
	if err != nil {
		return "", "", nil, errors.New("the local token was refused")
	}
	logger := l.Logger
	if logger == nil {
		logger = discardLogger
	}
	pub, err := newPublic(cfg, tokens, source{syncer: syncer, check: sourceDirCheck}, logger, api.DefaultActivityKeepalive)
	if err != nil {
		return "", "", nil, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	ready := platform.NewRegistry(0)
	pub.Start(runCtx, ready)
	bound := make(chan string, 1)
	srv := &platform.Server{
		Config: cfg, Logger: logger, Ready: ready, App: pub, Telemetry: l.Telemetry,
		OnListen: func(listener, a string) {
			if listener == "app" {
				bound <- a
			}
		},
	}
	var serveErr error
	finished := make(chan struct{})
	go func() {
		serveErr = srv.Serve(runCtx, l.Force)
		cancel()
		pub.loops.Wait()
		close(finished)
	}()
	wait = func() error {
		<-finished
		return serveErr
	}
	// OnListen runs before Serve can return, so a bound listener always wins.
	select {
	case a := <-bound:
		return "http://" + a, token, wait, nil
	case <-finished:
		select {
		case a := <-bound:
			return "http://" + a, token, wait, nil
		default:
		}
		if serveErr != nil {
			return "", "", nil, serveErr
		}
		return "", "", nil, errors.New("the local server stopped before it was ready")
	}
}

// newLocalToken is localTokenBytes from crypto/rand in lower-case hex.
func newLocalToken() (string, error) {
	b := make([]byte, localTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("cannot make a random token")
	}
	return hex.EncodeToString(b), nil
}

// noEnv is a Lookup with nothing set, for the built-in defaults.
func noEnv(string) (string, bool) { return "", false }

// StartLocal starts the server on loopback over the content at dir with a
// fresh random token and the built-in defaults, reading no GOLDIE_ variable.
// addr is the base URL a client talks to. stop shuts the listeners and the
// poll loop down without a drain and returns once they have stopped; it is
// safe to call more than once and concurrently. Cancelling ctx stops the same
// things. The embedded server of the client commands runs it.
func StartLocal(ctx context.Context, dir string) (addr, token string, stop func(), err error) {
	base, err := platform.LoadConfig(noEnv)
	if err != nil {
		return "", "", nil, err
	}
	base.DrainTimeout = 0
	runCtx, cancel := context.WithCancel(ctx)
	addr, token, wait, err := Local{Config: LocalConfig(base, dir)}.Start(runCtx)
	if err != nil {
		cancel()
		return "", "", nil, err
	}
	return addr, token, func() {
		cancel()
		_ = wait() // stop only waits; a failed shutdown leaves nothing running
	}, nil
}
