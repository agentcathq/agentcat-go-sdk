package diagnostics

import (
	"os"
	"strings"
	"testing"
)

// envFlag is the tri-state interpretation of DISABLE_DIAGNOSTICS.
type envFlag int

const (
	// envFlagUnset means the variable is absent or whitespace-only: no explicit
	// preference, so the caller decides (including auto-disabling under go test).
	envFlagUnset envFlag = iota
	// envFlagForceEnabled means an explicit falsy value (false/0/no/off): a
	// deliberate opt-in that keeps diagnostics on even inside a test binary.
	envFlagForceEnabled
	// envFlagDisabled means any other value: diagnostics are turned off.
	envFlagDisabled
)

// envDiagnosticsFlag interprets DISABLE_DIAGNOSTICS as a tri-state. Note this is
// subtly different from a plain boolean: an empty/whitespace-only value is "unset"
// (not the same as a falsy opt-in), so test detection can still apply to it.
func envDiagnosticsFlag() envFlag {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DISABLE_DIAGNOSTICS")))
	switch v {
	case "":
		return envFlagUnset
	case "false", "0", "no", "off":
		return envFlagForceEnabled
	default:
		return envFlagDisabled
	}
}

// isTestEnvironment reports whether the current process is a Go test binary.
// Importing testing from non-test code is safe: since Go 1.13 it registers no
// flags at import time, and testing.Testing() only reads state set by `go test`.
func isTestEnvironment() bool {
	return testing.Testing()
}

// resolveEndpoint returns the OTLP logs URL: DIAGNOSTICS_ENDPOINT or the default,
// with a single /v1/logs suffix.
func resolveEndpoint() string {
	base := DefaultDiagnosticsEndpoint
	if v := os.Getenv("DIAGNOSTICS_ENDPOINT"); v != "" {
		base = v
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1/logs") {
		return base
	}
	return base + "/v1/logs"
}

// resolveToken returns DIAGNOSTICS_TOKEN or the default shared token.
func resolveToken() string {
	if v := os.Getenv("DIAGNOSTICS_TOKEN"); v != "" {
		return v
	}
	return DefaultDiagnosticsToken
}
