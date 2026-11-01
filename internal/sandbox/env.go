package sandbox

import (
	"fmt"
	"regexp"
	"strings"
)

// Caller-supplied environment limits. They bound the Redis footprint of a job
// (up to 100 tests each carrying its own env) and the size of every go-judge
// /run request, which embeds the env once per Cmd.
const (
	MaxEnvVars       = 32
	MaxEnvValueBytes = 4 << 10
	MaxEnvTotalBytes = 8 << 10
)

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// deniedEnvPrefixes are variable families that make the dynamic loader or a
// language runtime load or run code before the submission starts, or change
// how the runtime reports resource exhaustion (e.g. JAVA_TOOL_OPTIONS=-Xmx
// turns a memory-limit verdict into a runtime error). go-judge confinement
// does not depend on the env, so this keeps verdicts deterministic rather
// than guarding the sandbox boundary itself.
var deniedEnvPrefixes = []string{
	"LD_",     // LD_PRELOAD, LD_LIBRARY_PATH, LD_AUDIT, ...
	"DYLD_",   // macOS loader equivalents; harmless on Linux, denied for clarity
	"MALLOC_", // glibc allocator tuning
	"PYTHON",  // PYTHONSTARTUP, PYTHONPATH, PYTHONHOME, ...
	"NODE_",   // NODE_OPTIONS=--require, NODE_PATH, ...
	"JAVA_",   // JAVA_TOOL_OPTIONS, JAVA_HOME
	"_JAVA_",  // _JAVA_OPTIONS
	"JDK_",    // JDK_JAVA_OPTIONS
}

var deniedEnvKeys = map[string]bool{
	"GLIBC_TUNABLES": true, // CVE-2023-4911 attack surface
	"GCONV_PATH":     true, // iconv module loading
	"LOCPATH":        true, // locale data loading
	"NLSPATH":        true, // message catalog loading
	"TMPDIR":         true,
	"IFS":            true,
}

// reservedEnvKeys are the keys the engine itself sets for any language
// (PATH, HOME, GOROOT, ...). Derived from Languages so a new language's env
// is protected without touching this file.
var reservedEnvKeys = func() map[string]bool {
	keys := make(map[string]bool)
	for _, lang := range Languages {
		for _, list := range [][]string{lang.CompileEnv, lang.RunEnv} {
			for _, kv := range list {
				if k, _, ok := strings.Cut(kv, "="); ok {
					keys[k] = true
				}
			}
		}
	}
	return keys
}()

// ValidateEnv checks a caller-supplied environment: every entry must be
// KEY=VALUE with a portable key, keys must be unique, must not be denied or
// engine-reserved, and the list must fit the size caps.
//
// Duplicates are rejected rather than resolved because glibc getenv returns
// the first match: the effective value would depend on list order.
func ValidateEnv(env []string) error {
	if len(env) > MaxEnvVars {
		return fmt.Errorf("too many variables (maximum %d allowed)", MaxEnvVars)
	}
	seen := make(map[string]bool, len(env))
	total := 0
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("entry %q must be KEY=VALUE", kv)
		}
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("key %q is invalid", key)
		}
		if strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("value for %q contains a NUL byte", key)
		}
		if len(value) > MaxEnvValueBytes {
			return fmt.Errorf("value for %q exceeds %d bytes", key, MaxEnvValueBytes)
		}
		if envKeyDenied(key) {
			return fmt.Errorf("key %q is not permitted", key)
		}
		if seen[key] {
			return fmt.Errorf("key %q is duplicated", key)
		}
		seen[key] = true
		total += len(kv)
		if total > MaxEnvTotalBytes {
			return fmt.Errorf("total size exceeds %d bytes", MaxEnvTotalBytes)
		}
	}
	return nil
}

func envKeyDenied(key string) bool {
	if deniedEnvKeys[key] || reservedEnvKeys[key] {
		return true
	}
	for _, p := range deniedEnvPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// MergeEnv returns base with overrides applied: an override replaces the base
// entry with the same key in place, and new keys are appended in order. Both
// inputs must already be valid (see ValidateEnv). Neither input is modified,
// but when one side is empty the other is returned as-is, so callers must
// treat the result as read-only.
func MergeEnv(base, overrides []string) []string {
	if len(overrides) == 0 {
		return base
	}
	if len(base) == 0 {
		return overrides
	}
	idx := make(map[string]int, len(base)+len(overrides))
	out := make([]string, 0, len(base)+len(overrides))
	for _, list := range [][]string{base, overrides} {
		for _, kv := range list {
			key, _, _ := strings.Cut(kv, "=")
			if i, ok := idx[key]; ok {
				out[i] = kv
				continue
			}
			idx[key] = len(out)
			out = append(out, kv)
		}
	}
	return out
}

// runEnv is the env for a submission run: the language's engine-owned
// variables followed by the caller's. ValidateEnv guarantees the two key sets
// are disjoint. A fresh slice is always built so a job's env can never be
// written into the backing array of the shared Languages config.
func runEnv(langEnv, extra []string) []string {
	out := make([]string, 0, len(langEnv)+len(extra))
	out = append(out, langEnv...)
	return append(out, extra...)
}
