package sandbox

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestValidateEnv(t *testing.T) {
	tooMany := make([]string, MaxEnvVars+1)
	for i := range tooMany {
		tooMany[i] = "K" + strconv.Itoa(i) + "=v"
	}
	// Each entry is under the per-value cap; together they exceed the total cap.
	half := strings.Repeat("x", MaxEnvTotalBytes/2)

	tests := []struct {
		name    string
		env     []string
		wantErr bool
	}{
		{"nil", nil, false},
		{"tz and lang", []string{"TZ=UTC", "LANG=en_US.UTF-8"}, false},
		{"problem flag", []string{"AC_MODE=strict", "_flag=1"}, false},
		{"empty value", []string{"EMPTY="}, false},
		{"equals in value", []string{"EXPR=a=b"}, false},

		{"no equals", []string{"TZ"}, true},
		{"empty key", []string{"=v"}, true},
		{"key starts with digit", []string{"1TZ=UTC"}, true},
		{"key with dash", []string{"MY-VAR=1"}, true},
		{"nul in value", []string{"TZ=UTC\x00LD_PRELOAD=x"}, true},
		{"duplicate key", []string{"TZ=UTC", "TZ=EST"}, true},
		{"too many", tooMany, true},
		{"value too long", []string{"V=" + strings.Repeat("x", MaxEnvValueBytes+1)}, true},
		{"total too long", []string{"A=" + half, "B=" + half}, true},

		{"LD_PRELOAD", []string{"LD_PRELOAD=/w/x.so"}, true},
		{"LD_LIBRARY_PATH", []string{"LD_LIBRARY_PATH=/w"}, true},
		{"GLIBC_TUNABLES", []string{"GLIBC_TUNABLES=glibc.malloc.check=1"}, true},
		{"MALLOC_", []string{"MALLOC_ARENA_MAX=1"}, true},
		{"JAVA_TOOL_OPTIONS", []string{"JAVA_TOOL_OPTIONS=-Xmx4g"}, true},
		{"_JAVA_OPTIONS", []string{"_JAVA_OPTIONS=-Xmx4g"}, true},
		{"JDK_JAVA_OPTIONS", []string{"JDK_JAVA_OPTIONS=-Xmx4g"}, true},
		{"PYTHONSTARTUP", []string{"PYTHONSTARTUP=/w/x.py"}, true},
		{"NODE_OPTIONS", []string{"NODE_OPTIONS=--require=/w/x.js"}, true},
		{"GCONV_PATH", []string{"GCONV_PATH=/w"}, true},

		// Engine-reserved: set by the language configs, so a caller must not shadow them.
		{"PATH", []string{"PATH=/w"}, true},
		{"HOME", []string{"HOME=/w"}, true},
		{"GOROOT", []string{"GOROOT=/w"}, true},
		{"KOTLIN_HOME", []string{"KOTLIN_HOME=/w"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEnv(tt.env)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEnv(%q) error = %v, wantErr = %v", tt.env, err, tt.wantErr)
			}
		})
	}
}

// Every key a language config sets must be rejected, so callers can never
// shadow an engine-owned variable, including in languages added later.
func TestValidateEnv_RejectsAllLanguageKeys(t *testing.T) {
	for name, lang := range Languages {
		for _, kv := range append(append([]string{}, lang.CompileEnv...), lang.RunEnv...) {
			if err := ValidateEnv([]string{kv}); err == nil {
				t.Errorf("%s: engine env %q was accepted", name, kv)
			}
		}
	}
}

func TestMergeEnv(t *testing.T) {
	tests := []struct {
		name            string
		base, overrides []string
		want            []string
	}{
		{"both nil", nil, nil, nil},
		{"base only", []string{"TZ=UTC"}, nil, []string{"TZ=UTC"}},
		{"overrides only", nil, []string{"TZ=UTC"}, []string{"TZ=UTC"}},
		{"append new keys", []string{"TZ=UTC"}, []string{"LANG=C"}, []string{"TZ=UTC", "LANG=C"}},
		{"override in place", []string{"TZ=UTC", "LANG=C"}, []string{"TZ=EST", "X=1"}, []string{"TZ=EST", "LANG=C", "X=1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := append([]string(nil), tt.base...)
			got := MergeEnv(tt.base, tt.overrides)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MergeEnv(%q, %q) = %q, want %q", tt.base, tt.overrides, got, tt.want)
			}
			if !reflect.DeepEqual(tt.base, base) {
				t.Errorf("MergeEnv modified base: %q, want %q", tt.base, base)
			}
		})
	}
}

func TestBuildRunCmd_AppendsCallerEnv(t *testing.T) {
	lang := Languages["java"]
	cmd := BuildRunCmdWithOptions(lang, "", "", "file-id", RunLimits{}, nil, []string{"TZ=UTC"})

	want := append(append([]string{}, lang.RunEnv...), "TZ=UTC")
	if !reflect.DeepEqual(cmd.Env, want) {
		t.Errorf("Env = %q, want %q", cmd.Env, want)
	}

	plain := BuildRunCmdWithOptions(lang, "", "", "file-id", RunLimits{}, nil, nil)
	if !reflect.DeepEqual(plain.Env, lang.RunEnv) {
		t.Errorf("Env without caller env = %q, want %q", plain.Env, lang.RunEnv)
	}
}

// A job's env must never be written into the shared Languages config, even if
// RunEnv has spare capacity (which a plain append would then reuse).
func TestBuildRunCmd_DoesNotAliasLanguageEnv(t *testing.T) {
	lang := Languages["python"]
	backing := make([]string, len(lang.RunEnv), len(lang.RunEnv)+4)
	copy(backing, lang.RunEnv)
	lang.RunEnv = backing

	a := BuildRunCmdWithOptions(lang, "", "", "file-id", RunLimits{}, nil, []string{"JOB=a"})
	b := BuildRunCmdWithOptions(lang, "", "", "file-id", RunLimits{}, nil, []string{"JOB=b"})

	if last := a.Env[len(a.Env)-1]; last != "JOB=a" {
		t.Errorf("first job's env was overwritten by the second: last entry = %q", last)
	}
	if last := b.Env[len(b.Env)-1]; last != "JOB=b" {
		t.Errorf("second job env last entry = %q, want JOB=b", last)
	}
	if extra := backing[:cap(backing)][len(backing)]; extra != "" {
		t.Errorf("caller env leaked into the language config's backing array: %q", extra)
	}
}
