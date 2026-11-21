// Command goldie is the single goldie binary. Its roles (server, mcp and the
// client commands) are wired together in internal/app.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return usageError()
		}
		_, err := fmt.Fprintln(out, platform.Version)
		return err
	case "server":
		if len(args) != 1 {
			return usageError()
		}
		return runServer(out)
	case "health":
		return runHealth(args[1:])
	}
	return usageError()
}

func usageError() error {
	return errors.New("usage: goldie server | health [--ready] | version")
}

// runServer starts the server and blocks until it stops. The first SIGTERM or
// SIGINT starts the drain; a second one exits immediately (PC-08).
func runServer(out io.Writer) error {
	cfg, err := platform.LoadConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	level, _ := platform.ParseLevel(cfg.LogLevel) // validated by LoadConfig
	logger := platform.NewLogger(out, level, "server")
	cfg.Report(logger)

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	force := make(chan struct{})
	go func() {
		<-sigs
		logger.Info("stop signal received")
		stop()
		<-sigs
		logger.Warn("second stop signal: exiting immediately")
		close(force)
	}()

	srv := &platform.Server{Config: cfg, Logger: logger, Ready: platform.NewRegistry(0)}
	return srv.Serve(ctx, force)
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
		return fmt.Errorf("%s: %w", platform.EnvAdminAddr, err)
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
