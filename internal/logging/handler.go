// Package logging builds the process-wide slog handler for each AC_LOG_FORMAT.
package logging

import (
	"io"
	"log/slog"
)

// Supported AC_LOG_FORMAT values.
const (
	FormatJSON       = "json"       // slog's default JSON keys: time, level, msg
	FormatText       = "text"       // human-readable key=value lines for local development
	FormatGCP        = "gcp"        // Google Cloud Logging: severity, message, time
	FormatCloudWatch = "cloudwatch" // AWS CloudWatch Logs: level, message, timestamp
)

// ValidFormat reports whether s names a supported log format. The empty string
// is valid and means json.
func ValidFormat(s string) bool {
	switch s {
	case "", FormatJSON, FormatText, FormatGCP, FormatCloudWatch:
		return true
	}
	return false
}

// NewHandler returns a handler that writes records at or above level to w in
// the given format. Unknown formats fall back to json; config.Validate rejects
// them before this is called.
func NewHandler(format string, level slog.Leveler, w io.Writer) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	switch format {
	case FormatText:
		return slog.NewTextHandler(w, opts)
	case FormatGCP:
		opts.ReplaceAttr = gcpAttr
	case FormatCloudWatch:
		opts.ReplaceAttr = cloudWatchAttr
	}
	return slog.NewJSONHandler(w, opts)
}

// gcpAttr renames the built-in keys to the fields Cloud Logging lifts out of a
// JSON payload. time is kept as-is: Cloud Logging already reads an RFC3339
// "time" string, whereas "timestamp" must be a {seconds, nanos} object.
func gcpAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a // only top-level built-ins are renamed, never user attrs in a group
	}
	switch a.Key {
	case slog.LevelKey:
		level, _ := a.Value.Any().(slog.Level)
		return slog.String("severity", gcpSeverity(level))
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}

// gcpSeverity maps a slog level to a Cloud Logging LogSeverity name. Levels
// between the named ones (e.g. slog.LevelInfo+2) round down, matching how slog
// itself prints them ("INFO+2").
func gcpSeverity(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "DEBUG"
	case l < slog.LevelWarn:
		return "INFO"
	case l < slog.LevelError:
		return "WARNING"
	default:
		return "ERROR"
	}
}

// cloudWatchAttr renames the built-in keys to the field names AWS uses for its
// own structured JSON logs. level keeps slog's values (DEBUG/INFO/WARN/ERROR).
func cloudWatchAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		a.Key = "timestamp"
	case slog.MessageKey:
		a.Key = "message"
	}
	return a
}
