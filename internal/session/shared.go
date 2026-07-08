package session

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/segmentio/ksuid"
	"go.agentcat.com/sdk/internal/core"
)

const (
	// epoch2024Ms is 2024-01-01T00:00:00Z in Unix milliseconds, the fixed
	// epoch used for deterministic session ID timestamps.
	epoch2024Ms = 1704067200000

	// maxTimestampOffsetMs caps the deterministic timestamp offset at one year.
	maxTimestampOffsetMs = 365 * 24 * 60 * 60 * 1000
)

// GenerateSessionID generates a new session ID with the AgentCat session prefix.
func GenerateSessionID() string {
	return fmt.Sprintf("%s_%s", core.PrefixSession, ksuid.New().String())
}

// DeriveSessionIDFromMCPSession creates a deterministic session KSUID from an
// MCP transport session ID and optional project ID. The same inputs always
// produce the same session ID, enabling correlation across server restarts.
//
// The derivation matches the TypeScript SDK exactly: SHA-256 over
// "<mcpSessionID>:<projectID>" (or just the session ID when projectID is
// empty), a timestamp of 2024-01-01 plus the first 4 hash bytes as a
// millisecond offset (capped at one year), and hash bytes 4..20 as the
// KSUID payload.
func DeriveSessionIDFromMCPSession(mcpSessionID, projectID string) string {
	input := mcpSessionID
	if projectID != "" {
		input = mcpSessionID + ":" + projectID
	}

	hash := sha256.Sum256([]byte(input))

	offsetMs := int64(binary.BigEndian.Uint32(hash[0:4])) % maxTimestampOffsetMs
	timestampMs := int64(epoch2024Ms) + offsetMs

	id, err := ksuid.FromParts(time.UnixMilli(timestampMs), hash[4:20])
	if err != nil {
		// Fail open: fall back to a random session ID rather than erroring.
		return GenerateSessionID()
	}

	return fmt.Sprintf("%s_%s", core.PrefixSession, id.String())
}

// GetDependencyVersion returns the version of the given module from build info,
// or "dev" if the module is not found.
func GetDependencyVersion(modulePath string) string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == modulePath {
				return dep.Version
			}
		}
	}
	return "dev"
}
