package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

// The server hands the public handler its own component logger, so a
// recovered /mcp panic lands in the operator's log (#168), and every setting
// reaches the handler as LoadConfig read it.
func TestPublicOptionsCarryTheServerLoggerAndSettings(t *testing.T) {
	cfg := platform.Config{
		SwaggerUIEnabled: true,
		ContentDir:       "/content",
		APIToken:         "operator-token",
		APITokens:        "ci:reader:reader-token",
		RateLimit:        platform.RateLimit{Requests: 7, Per: time.Minute, Burst: 3},
	}
	logger := platform.NewLogger(io.Discard, slog.LevelInfo, "server")
	opts := publicOptions(cfg, logger)
	if opts.Logger != logger {
		t.Error("the public handler does not get the server's logger")
	}
	if !opts.SwaggerUI || opts.ContentDir != cfg.ContentDir || opts.APIToken != cfg.APIToken ||
		opts.APITokens != cfg.APITokens || opts.RateLimit != cfg.RateLimit {
		t.Errorf("the public handler settings differ from the config: %+v", opts)
	}
}
