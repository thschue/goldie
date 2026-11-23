// Command goldie is the single goldie binary. Its roles (server, mcp and the
// client commands) are wired together in internal/app.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thschue/goldie/examples"
	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/platform"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "goldie:", err)
		os.Exit(1)
	}
}

// run is the testable entry point.
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return usageError()
	}
	if cli.IsCommand(args[0]) {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		env, flush, err := clientEnv(out, platform.RoleCLI)
		if err != nil {
			return err
		}
		defer flush()
		return cli.Run(ctx, args, env)
	}
	switch args[0] {
	case "server":
		switch {
		case len(args) == 1:
			return runServer(out)
		case args[1] == "-dev" && len(args) <= 3:
			return runDev(args[2:], out)
		}
		return usageError()
	case "validate":
		if len(args) > 2 {
			return usageError()
		}
		dir, _ := os.LookupEnv(platform.EnvContentDir)
		if len(args) == 2 {
			dir = args[1]
		}
		warnings, err := app.ValidateDirWarnings(context.Background(), dir)
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, "goldie: warning:", w)
		}
		return err
	case "health":
		return runHealth(args[1:])
	case "mcp":
		return runMCP(args[1:])
	}
	return usageError()
}

// clientEnv is the process environment for the client role (RoleCLI or RoleMCP), with the
// embedded server of --dir and GOLDIE_DIR wired in from app, so cli imports
// neither engine nor app. When OTEL_* selects traces, its client traces every
// API call. The caller defers flush, which sends the spans within clientFlush.
func clientEnv(out io.Writer, role string) (env cli.Env, flush func(), err error) {
	env = cli.DefaultEnv(out)
	env.StartLocal = func(ctx context.Context, dir string) (string, string, func(), error) {
		return app.StartLocal(ctx, app.LocalOptions{BaseDir: dir})
	}
	env.StartLocalScope = func(ctx context.Context, dir, goldieDir string) (string, string, func(), error) {
		return app.StartLocal(ctx, app.LocalOptions{BaseDir: dir, GoldieDir: goldieDir})
	}
	tel, err := platform.SetupClientTelemetry(context.Background(), os.LookupEnv, role, platform.WithSpanPath(app.ClientSpanPath))
	if err != nil {
		return env, nil, fmt.Errorf("invalid configuration:\n%w", err)
	}
	if tel.Enabled() {
		// The SDK's notices go to stderr: stdout carries the command's
		// output, or for goldie mcp only MCP messages.
		tel.RouteNoticesTo(platform.NewLogger(os.Stderr, slog.LevelInfo, role))
		env.Client = &http.Client{Transport: tel.WrapTransport(http.DefaultTransport)}
	}
	return env, func() { flushTelemetry(tel, clientFlush) }, nil
}

func usageError() error {
	return errors.New("usage: goldie server [-dev [dir]] | validate [dir] | health [--ready] | " + app.MCPUsage + " | " + cli.Usage + "\n" +
		cli.TokenNote + "\n" + devNote)
}

// devNote explains server -dev in the usage, which names the program once.
const devNote = "server -dev serves dir (else GOLDIE_CONTENT_DIR, else the working directory) on 127.0.0.1 " +
	"with a random token and prints the export line for the client commands; it ignores GOLDIE_ADDR, " +
	"GOLDIE_ADMIN_ADDR and the API token variables"

// runMCP serves MCP on stdin and stdout until the client closes stdin or a
// SIGTERM or SIGINT arrives. Errors go to stderr through main: stdout carries
// only MCP messages.
func runMCP(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env, flush, err := clientEnv(io.Discard, platform.RoleMCP)
	if err != nil {
		return err
	}
	defer flush()
	return app.RunMCP(ctx, args, env, os.Stdin, os.Stdout)
}

// runServer starts the server and blocks until it stops. The first SIGTERM or
// SIGINT starts the drain; a second one exits immediately (PC-08).
func runServer(out io.Writer) error {
	cfg, logger, tel, err := serverSetup(os.LookupEnv, out)
	if err != nil {
		return err
	}
	defer flushTelemetry(tel, serverFlush)
	cfg.Report(logger)
	tel.Report(logger)
	ctx, force, stop := stopSignals(logger)
	defer stop()

	// The public handler gets the server's component logger, so what it logs
	// reaches the operator's log.
	public, err := app.PublicHandler(cfg, logger)
	if err != nil {
		return withDevHint(cfg, handlerError(err))
	}
	ready := platform.NewRegistry(0)
	public.Start(ctx, ready)
	srv := &platform.Server{Config: cfg, Logger: logger, Ready: ready, App: public, Telemetry: tel}
	return srv.Serve(ctx, force)
}

// runDev runs goldie server -dev [dir]: the server on loopback over dir, else
// GOLDIE_CONTENT_DIR, else the working directory, with a random token. It
// prints the export line once, straight to out: the token goes through no
// logger, so it never reaches a log line or the OTLP log bridge. Log records
// go to stderr, so stdout carries only the export line and is safe to eval
// (PC-07).
func runDev(args []string, out io.Writer) error {
	lookup, ignored := app.DevLookup(os.LookupEnv)
	cfg, logger, tel, err := serverSetup(lookup, os.Stderr)
	if err != nil {
		return err
	}
	defer flushTelemetry(tel, serverFlush)
	dir := cfg.ContentDir
	if len(args) == 1 {
		dir = args[0]
	}
	if dir == "" {
		dir = "."
	}
	cfg = app.LocalConfig(cfg, dir)
	if !isSet(lookup, platform.EnvDrainSeconds) {
		cfg.DrainTimeout = 0 // nothing balances load in front of -dev: stop at once
	}
	cfg.Report(logger)
	tel.Report(logger)
	if ignored {
		logger.Info(app.DevIgnoredMessage)
	}
	ctx, force, stop := stopSignals(logger)
	defer stop()

	addr, token, wait, err := app.Local{Config: cfg, Logger: logger, Telemetry: tel, Force: force, Examples: examples.Knowledge()}.Start(ctx)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "export %s=%s %s=%s\n", platform.EnvAddr, addr, platform.EnvToken, token); err != nil {
		stop()
		_ = wait() // the print failure is what the caller needs to know
		return errors.New("cannot print the export line")
	}
	return wait()
}

// isSet reports whether lookup has name or name_FILE, even empty.
func isSet(lookup platform.Lookup, name string) bool {
	_, ok := lookup(name)
	_, okFile := lookup(name + "_FILE")
	return ok || okFile
}

// serverSetup loads the configuration through lookup and builds the server's
// logger, writing to logOut, and telemetry. The caller defers flushTelemetry.
func serverSetup(lookup platform.Lookup, logOut io.Writer) (platform.Config, *slog.Logger, *platform.Telemetry, error) {
	cfg, err := platform.LoadConfig(lookup)
	if err != nil {
		return cfg, nil, nil, fmt.Errorf("invalid configuration:\n%w", err)
	}
	level, _ := platform.ParseLevel(cfg.LogLevel) // validated by LoadConfig
	logger := platform.NewLogger(logOut, level, "server")
	tel, err := platform.SetupTelemetry(context.Background(), os.LookupEnv, platform.WithSpanPath(app.SpanPath))
	if err != nil {
		return cfg, nil, nil, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return cfg, tel.WrapLogger(logger, "server"), tel, nil
}

// How long flushTelemetry may take. A client role flushes on its way out, so
// an unreachable collector delays its exit by at most clientFlush.
const (
	serverFlush = 5 * time.Second
	clientFlush = 2 * time.Second
)

// flushTelemetry flushes and stops telemetry within timeout. For the server,
// Serve flushes it in the drain; this covers early returns.
func flushTelemetry(tel *platform.Telemetry, timeout time.Duration) {
	fctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = tel.Shutdown(fctx)
}

// stopSignals returns a context the first SIGTERM or SIGINT cancels and a
// channel the second one closes (PC-08). stop releases the signals.
func stopSignals(logger *slog.Logger) (ctx context.Context, force <-chan struct{}, stop func()) {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	ctx, cancel := context.WithCancel(context.Background())
	forced := make(chan struct{})
	go func() {
		<-sigs
		logger.Info("stop signal received")
		cancel()
		<-sigs
		logger.Warn("second stop signal: exiting immediately")
		close(forced)
	}()
	return ctx, forced, func() {
		signal.Stop(sigs)
		cancel()
	}
}

// handlerError labels a PublicHandler failure: a settings problem is invalid
// configuration, anything else is an internal failure to build the handler.
func handlerError(err error) error {
	var cfgErr *app.ConfigError
	if errors.As(err, &cfgErr) {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	return fmt.Errorf("build public handler: %w", err)
}

// firstRunHint is the next step for someone who ran goldie server with nothing set.
const firstRunHint = "To try goldie on this machine: goldie server -dev ."

// withDevHint ends err with firstRunHint when cfg names neither a Source nor an
// API token: a first run, not an operator mid-setup. A value that is set but
// invalid, or only one of the two missing, keeps the error as it is.
func withDevHint(cfg platform.Config, err error) error {
	if cfg.ContentDir != "" || cfg.GitURL != "" || cfg.APIToken != "" || cfg.APITokens != "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, firstRunHint)
}

// runHealth probes the local admin port (PC-09). It checks /livez, or /readyz
// with --ready, and returns an error (exit 1) unless the answer is 200.
func runHealth(args []string) error {
	path := "/livez"
	for _, a := range args {
		if a != "--ready" {
			return usageError()
		}
		path = "/readyz"
	}
	cfg, err := platform.LoadConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	host, port, err := net.SplitHostPort(cfg.AdminAddr)
	if err != nil {
		return fmt.Errorf("%s: must be a host:port address", platform.EnvAdminAddr)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + path)
	if err != nil {
		return errors.New("unhealthy: admin port unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s answered %d", path, resp.StatusCode)
	}
	return nil
}
