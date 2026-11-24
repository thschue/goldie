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
	"strings"
	"syscall"
	"time"

	"github.com/thschue/goldie/examples"
	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

func main() {
	os.Exit(exitCode(run(os.Args[1:], os.Stdout), os.Stderr))
}

// exitCode reports err to w and returns the exit code: 0 for none, 2 for a
// command used wrongly (an error line and the next step), 1 for other failures.
func exitCode(err error, w io.Writer) int {
	var u *usageErr
	switch {
	case err == nil:
		return 0
	case errors.As(err, &u):
		next := "Run '" + u.path + " --help'."
		_, _ = fmt.Fprintf(w, "goldie: %s\n%s\n", u.msg, next)
		return 2
	}
	_, _ = fmt.Fprintln(w, "goldie:", err)
	if errors.Is(err, cli.ErrNoAddress) {
		// A first run: nothing to ask, so say how to start something.
		_, _ = fmt.Fprintln(w, platform.FirstRunHint)
	}
	return 1
}

// run is the testable entry point: results and asked-for help go to out.
func run(args []string, out io.Writer) error {
	root := newRoot(out)
	if len(args) == 1 && args[0] == "--version" {
		args = []string{"version"}
	}
	// An unknown command is misuse even with --help.
	if _, _, err := root.Find(args); err != nil {
		// A flag before the command is what is wrong, not the word after it.
		if len(args) > 0 && strings.HasPrefix(args[0], "-") {
			root.InitDefaultHelpFlag()
			if ferr := root.ParseFlags(args); ferr != nil {
				return root.FlagErrorFunc()(root, ferr)
			}
		}
		return newUsageErr(root.CommandPath(), fmt.Sprintf("unknown command %q", firstWord(args)))
	}
	if args == nil {
		args = []string{} // nil would make cobra read os.Args
	}
	root.SetArgs(args)
	return root.Execute()
}

// rootName is the path content was opened with, which Local shows in notices;
// "" for no content directory.
func rootName(content *os.Root) string {
	if content == nil {
		return ""
	}
	return content.Name()
}

// clientEnv is the process environment for the client role (RoleCLI or RoleMCP), with the
// embedded server of --dir and GOLDIE_DIR wired in from app, so cli imports
// neither engine nor app. When OTEL_* selects traces, its client traces every
// API call. The caller defers flush, which sends the spans within clientFlush.
func clientEnv(out io.Writer, role string) (env cli.Env, flush func(), err error) {
	env = cli.DefaultEnv(out)
	env.StartLocal = func(ctx context.Context, content *os.Root) (string, string, func(), error) {
		return app.StartLocal(ctx, app.LocalOptions{BaseDir: rootName(content), Root: content})
	}
	env.StartLocalScope = func(ctx context.Context, content *os.Root, goldieDir string) (string, string, func(), error) {
		return app.StartLocal(ctx, app.LocalOptions{BaseDir: rootName(content), Root: content, GoldieDir: goldieDir})
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

// runServer starts the server and blocks until it stops. The first SIGTERM or
// SIGINT starts the drain; a second one exits immediately (PC-08).
func runServer(out io.Writer, layers *platform.Layers) error {
	cfg, logger, tel, err := serverSetup(layers.Lookup(), layers.Annotate, out)
	if err != nil {
		return err
	}
	defer flushTelemetry(tel, serverFlush)
	cfg.ReportWithSources(logger, layers.Source)
	tel.Report(logger)
	ctx, force, stop := stopSignals(logger)
	defer stop()

	// The public handler gets the server's component logger, so what it logs
	// reaches the operator's log.
	public, err := app.PublicHandlerWith(cfg, logger, tel)
	if err != nil {
		return withDevHint(cfg, handlerError(err))
	}
	ready := platform.NewRegistry(0)
	public.Start(ctx, ready)
	srv := &platform.Server{Config: cfg, Logger: logger, Ready: ready, App: public, Telemetry: tel}
	return srv.Serve(ctx, force)
}

// runDev runs goldie server --dev [dir]: the server on loopback over dir, else
// GOLDIE_CONTENT_DIR, else the working directory, with a random token. It
// prints the export line once, straight to out: the token goes through no
// logger, so it never reaches a log line or the OTLP log bridge. Log records
// go to stderr, so stdout carries only the export line and is safe to eval
// (PC-07).
func runDev(args []string, out io.Writer, layers *platform.Layers, git bool) error {
	lookup, ignored := app.DevLookup(layers.Lookup())
	cfg, logger, tel, err := serverSetup(lookup, layers.Annotate, os.Stderr)
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
	if git {
		dir = ""
	}
	cfg = app.LocalConfig(cfg, dir)
	if !isSet(lookup, platform.EnvDrainSeconds) {
		cfg.DrainTimeout = 0 // nothing balances load in front of --dev: stop at once
	}
	cfg.Report(logger)
	tel.Report(logger)
	if ignored {
		logger.Info(app.DevIgnoredMessage)
	}
	ctx, force, stop := stopSignals(logger)
	defer stop()

	addr, token, wait, err := app.Local{Config: cfg, Logger: logger, Telemetry: tel, Force: force, Examples: examples.Knowledge(), Git: git}.Start(ctx)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "export %s=%s %s=%s\n", platform.EnvAddr, addr, platform.EnvToken, token); err != nil {
		stop()
		_ = wait() // the print failure is what the caller needs to know
		return errors.New("cannot print the export line")
	}
	go offerSetup(ctx, logger, addr, token)
	return wait()
}

// offerSetup offers to set up the repository for the dev server (#801), only
// when stdin and stderr are both terminals. It runs beside wait(): the first
// signal cancels ctx and ends the wait for an answer. Its flow writes to
// stderr, so stdout stays the export line (PC-07). A failure is logged without
// the token, which init never words.
func offerSetup(ctx context.Context, logger *slog.Logger, addr, token string) {
	env := cli.DefaultEnv(os.Stderr)
	if env.In == nil {
		return
	}
	if fi, err := os.Stderr.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	if err := cli.OfferInit(ctx, env, addr, token); err != nil && ctx.Err() == nil {
		logger.Warn("goldie setup stopped", "reason", contract.CleanLine(err.Error()))
	}
}

// isSet reports whether lookup has name or name_FILE, even empty.
func isSet(lookup platform.Lookup, name string) bool {
	_, ok := lookup(name)
	_, okFile := lookup(name + "_FILE")
	return ok || okFile
}

// serverSetup loads the configuration through lookup and builds the server's
// logger, writing to logOut, and telemetry. annotate adds where each value
// the errors name came from. The caller defers flushTelemetry.
func serverSetup(lookup platform.Lookup, annotate func(error) error, logOut io.Writer) (platform.Config, *slog.Logger, *platform.Telemetry, error) {
	cfg, err := platform.LoadConfig(lookup)
	if err != nil {
		return cfg, nil, nil, fmt.Errorf("invalid configuration:\n%w", annotate(err))
	}
	level, _ := platform.ParseLevel(cfg.LogLevel) // validated by LoadConfig
	logger := platform.NewLogger(logOut, level, "server")
	tel, err := platform.SetupTelemetry(context.Background(), lookup, platform.WithSpanPath(app.SpanPath))
	if err != nil {
		return cfg, nil, nil, fmt.Errorf("invalid configuration:\n%w", annotate(err))
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

// withDevHint ends err with platform.FirstRunHint when cfg names neither a Source nor an
// API token: a first run, not an operator mid-setup. A value that is set but
// invalid, or only one of the two missing, keeps the error as it is. A ui-mode
// server serves no content by design, so it never gets the hint.
func withDevHint(cfg platform.Config, err error) error {
	if cfg.Mode == platform.ModeUI || cfg.Mode == platform.ModeMCP || cfg.ContentDir != "" || cfg.GitURL != "" || cfg.APIToken != "" || cfg.APITokens != "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, platform.FirstRunHint)
}

// healthCheckHint is the line after an unreachable admin port.
const healthCheckHint = "Check that goldie server is running and that " + platform.EnvAdminAddr + " matches its admin address."

// runHealth probes the local admin port (PC-09). It checks /livez, or /readyz
// when ready, and returns an error (exit 1) unless the answer is 200.
func runHealth(ready bool) error {
	path := "/livez"
	if ready {
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
		return fmt.Errorf("unhealthy: admin port unreachable\n%s", healthCheckHint)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s answered %d", path, resp.StatusCode)
	}
	return nil
}
