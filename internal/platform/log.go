package platform

import (
	"errors"
	"io"
	"log/slog"
	"strings"
)

// ParseLevel maps GOLDIE_LOG_LEVEL to a slog level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	// The value is not echoed: it may have been read from a _FILE (PC-05).
	return 0, errors.New("must be one of debug, info, warn, error")
}

// NewLogger returns a JSON logger writing to w. Every line carries ts,
// level, msg, component and version (PC-07).
func NewLogger(w io.Writer, level slog.Level, component string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.TimeKey:
				a.Key = "ts"
			case slog.LevelKey:
				a.Value = slog.StringValue(strings.ToLower(a.Value.String()))
			}
			return a
		},
	})
	return slog.New(h).With("component", component, "version", Version)
}
