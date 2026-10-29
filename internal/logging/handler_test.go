package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// logOne writes a single record through NewHandler and decodes the JSON line.
func logOne(t *testing.T, format string, level slog.Level, msg string, args ...any) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(NewHandler(format, slog.LevelDebug-4, &buf))
	logger.Log(context.Background(), level, msg, args...)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("%s output is not JSON: %v\n%s", format, err, buf.String())
	}
	return rec
}

func assertKeys(t *testing.T, rec map[string]any, present, absent []string) {
	t.Helper()
	for _, k := range present {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing key %q in %v", k, rec)
		}
	}
	for _, k := range absent {
		if _, ok := rec[k]; ok {
			t.Errorf("unexpected key %q in %v", k, rec)
		}
	}
}

func TestJSONKeepsDefaultKeys(t *testing.T) {
	for _, format := range []string{"", FormatJSON} {
		rec := logOne(t, format, slog.LevelWarn, "hello")
		assertKeys(t, rec, []string{"time", "level", "msg"}, []string{"severity", "message", "timestamp"})
		if rec["level"] != "WARN" || rec["msg"] != "hello" {
			t.Fatalf("format %q: got level=%v msg=%v", format, rec["level"], rec["msg"])
		}
	}
}

func TestGCPFieldNames(t *testing.T) {
	rec := logOne(t, FormatGCP, slog.LevelInfo, "job done", "submission_id", "abc")
	assertKeys(t, rec, []string{"time", "severity", "message", "submission_id"}, []string{"level", "msg", "timestamp"})
	if rec["message"] != "job done" || rec["submission_id"] != "abc" {
		t.Fatalf("got message=%v submission_id=%v", rec["message"], rec["submission_id"])
	}
}

func TestGCPSeverityMapping(t *testing.T) {
	cases := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug - 4, "DEBUG"},
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelInfo + 2, "INFO"},
		{slog.LevelWarn, "WARNING"},
		{slog.LevelError, "ERROR"},
		{slog.LevelError + 4, "ERROR"},
	}
	for _, tc := range cases {
		rec := logOne(t, FormatGCP, tc.level, "m")
		if rec["severity"] != tc.want {
			t.Errorf("level %v: severity = %v, want %s", tc.level, rec["severity"], tc.want)
		}
	}
}

func TestGCPKeepsUserLevelAttr(t *testing.T) {
	rec := logOne(t, FormatGCP, slog.LevelError, "boom", "level", "fatal")
	if rec["severity"] != "ERROR" || rec["level"] != "fatal" {
		t.Fatalf("got severity=%v level=%v, want ERROR and fatal", rec["severity"], rec["level"])
	}
}

func TestCloudWatchFieldNames(t *testing.T) {
	rec := logOne(t, FormatCloudWatch, slog.LevelError, "boom")
	assertKeys(t, rec, []string{"timestamp", "level", "message"}, []string{"time", "msg", "severity"})
	if rec["level"] != "ERROR" || rec["message"] != "boom" {
		t.Fatalf("got level=%v message=%v", rec["level"], rec["message"])
	}
}

func TestGroupedAttrsAreNotRenamed(t *testing.T) {
	for _, format := range []string{FormatGCP, FormatCloudWatch} {
		var buf bytes.Buffer
		slog.New(NewHandler(format, slog.LevelInfo, &buf)).
			WithGroup("req").Info("outer", "msg", "inner", "time", "t0", "level", "l0")
		var rec struct {
			Req map[string]any `json:"req"`
		}
		if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		assertKeys(t, rec.Req, []string{"msg", "time", "level"}, []string{"message", "timestamp", "severity"})
	}
}

func TestLevelFilterApplies(t *testing.T) {
	for _, format := range []string{FormatJSON, FormatText, FormatGCP, FormatCloudWatch} {
		var buf bytes.Buffer
		logger := slog.New(NewHandler(format, slog.LevelWarn, &buf))
		logger.Info("dropped")
		logger.Warn("kept-warn")
		logger.Error("kept-error")
		out := buf.String()
		if strings.Contains(out, "dropped") {
			t.Errorf("%s: info record written below warn level: %s", format, out)
		}
		if !strings.Contains(out, "kept-warn") || !strings.Contains(out, "kept-error") {
			t.Errorf("%s: warn/error records missing at warn level: %s", format, out)
		}
	}
}

func TestTextFormat(t *testing.T) {
	var buf bytes.Buffer
	slog.New(NewHandler(FormatText, slog.LevelInfo, &buf)).Info("hi")
	if out := buf.String(); !strings.Contains(out, "level=INFO") || !strings.Contains(out, "msg=hi") {
		t.Fatalf("unexpected text output: %q", out)
	}
}

func TestValidFormat(t *testing.T) {
	for _, s := range []string{"", FormatJSON, FormatText, FormatGCP, FormatCloudWatch} {
		if !ValidFormat(s) {
			t.Errorf("ValidFormat(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"gpc", "JSON", "stackdriver", " json"} {
		if ValidFormat(s) {
			t.Errorf("ValidFormat(%q) = true, want false", s)
		}
	}
}
