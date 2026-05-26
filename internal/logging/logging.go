// Package logging provides a small wrapper around log/slog so the rest of the
// codebase has one canonical way to construct loggers. The wrapper exists for
// two reasons: (1) so we can swap implementations later without touching every
// call site, and (2) so the CLI and HTTP server can install matching handlers
// (text for terminals, JSON for production).
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format selects the slog handler.
type Format int

const (
	FormatAuto Format = iota
	FormatText
	FormatJSON
)

// Options configures the logger built by New.
type Options struct {
	Level  slog.Level
	Format Format
	Out    io.Writer // defaults to os.Stderr
}

// New returns a slog.Logger configured per opts. If opts.Out is nil, os.Stderr
// is used. FormatAuto picks text when the writer looks like a TTY, JSON
// otherwise.
func New(opts Options) *slog.Logger {
	out := opts.Out
	if out == nil {
		out = os.Stderr
	}

	format := opts.Format
	if format == FormatAuto {
		if isTerminal(out) {
			format = FormatText
		} else {
			format = FormatJSON
		}
	}

	handlerOpts := &slog.HandlerOptions{Level: opts.Level}
	var handler slog.Handler
	switch format {
	case FormatJSON:
		handler = slog.NewJSONHandler(out, handlerOpts)
	default:
		handler = slog.NewTextHandler(out, handlerOpts)
	}
	return slog.New(handler)
}

// ParseLevel converts a level string (debug/info/warn/error) into slog.Level.
// Unknown values fall back to info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// isTerminal returns true if w is an *os.File pointing at a terminal.
// Kept here (rather than depending on golang.org/x/term) to keep Phase 0
// dependency-free; we'll swap in the proper isatty later if needed.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
