// Package handles implements AgentCat's stateless session/agent handle
// primitives: minting, deterministic derivation, extraction, resolution,
// and mint-back text. Nothing here stores state between requests.
package handles

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/segmentio/ksuid"
	"go.agentcat.com/sdk/v2/internal/constants"
	"go.agentcat.com/sdk/v2/internal/logging"
)

// SessionSource records how a session ID was obtained for a call.
type SessionSource string

const (
	SessionSourceSupplied SessionSource = "supplied"
	SessionSourceMinted   SessionSource = "minted"
	SessionSourceHook     SessionSource = "hook"
	// SessionSourceInvalid: the session_id parameter is AgentCat's, but the
	// value the agent sent is not one this server issued. The event publishes
	// sessionless — Event.SessionId is exempt from both redaction hooks, so an
	// unvalidated value adopted there could never be scrubbed afterwards.
	SessionSourceInvalid SessionSource = "invalid"
	// SessionSourceForeign: the session_id parameter belongs to the customer's
	// tool. AgentCat never injected one there, so nothing in the arguments is
	// ours to read and nothing is said to the agent about it.
	SessionSourceForeign SessionSource = "foreign"
)

// SessionResolution is the outcome of per-call stateless session resolution.
type SessionResolution struct {
	// SessionID is "" when the call publishes sessionless (invalid, foreign).
	SessionID string
	Source    SessionSource
	// HookMode reports that ResolveSessionID is configured: no session_id was
	// injected anywhere, so the agent must never be prompted about one.
	HookMode bool
}

const (
	// epoch2024Ms is 2024-01-01T00:00:00Z in Unix milliseconds, the fixed
	// epoch used for deterministic session ID timestamps.
	epoch2024Ms = 1704067200000
	// maxTimestampOffsetMs caps the deterministic timestamp offset at one year.
	maxTimestampOffsetMs = 365 * 24 * 60 * 60 * 1000
)

var logger = logging.New()

// MintSessionID returns a new random ses_-prefixed session ID.
func MintSessionID() string {
	return fmt.Sprintf("%s_%s", constants.PrefixSession, ksuid.New().String())
}

// DeriveSessionID deterministically derives a ses_ KSUID from a
// customer-supplied correlation ID and optional project ID. Matches the
// TypeScript SDK byte-for-byte (see golden vectors). It does NOT trim —
// callers trim first.
func DeriveSessionID(customerID, projectID string) string {
	input := customerID
	if projectID != "" {
		input = customerID + ":" + projectID
	}
	hash := sha256.Sum256([]byte(input))
	offsetMs := int64(binary.BigEndian.Uint32(hash[0:4])) % maxTimestampOffsetMs
	id, err := ksuid.FromParts(time.UnixMilli(epoch2024Ms+offsetMs), hash[4:20])
	if err != nil {
		return MintSessionID() // fail open
	}
	return fmt.Sprintf("%s_%s", constants.PrefixSession, id.String())
}

// ExtractHandle returns args[name] when it is a string whose trimmed value
// is non-empty. The returned value is VERBATIM (untrimmed).
//
// This is deliberately shape-agnostic: it is shared with agent_id, which the
// agent invents and this SDK never validates. Shape checking belongs to the
// caller — ResolveSessionHandle trims and applies IsValidSessionID to the
// session handle, which is the one field redaction cannot reach.
func ExtractHandle(args map[string]any, name string) (string, bool) {
	v, ok := args[name].(string)
	if !ok || strings.TrimSpace(v) == "" {
		return "", false
	}
	return v, true
}

// validSessionID is the shape of every ID this SDK issues: the ses_ prefix
// followed by a 27-character base62 KSUID.
var validSessionID = regexp.MustCompile(`^` + constants.PrefixSession + `_[0-9A-Za-z]{27}$`)

// IsValidSessionID reports whether value is a session ID this SDK issued.
// Both issuing paths — MintSessionID and DeriveSessionID — satisfy it by
// construction, so a value that fails was invented by the agent or belongs to
// someone else. AgentCat trusts only handles it issued: Event.SessionId
// bypasses RedactFunc and RedactEvent alike, so a value adopted there reaches
// every configured exporter with no opportunity to scrub it.
func IsValidSessionID(value string) bool { return validSessionID.MatchString(value) }

// ResolveSessionHandle statelessly resolves the session ID for one call.
//
// hook is non-nil in hook mode (the adapter closes over its ResolveSessionID
// callback); in hook mode the supplied args are ignored entirely.
//
// sessionParamIsOurs reports whether the session_id argument on this call is
// AgentCat's to read — false when the customer's own tool declares that
// parameter. Callers derive it with inject.SessionParamIsOurs; it is a
// positional argument rather than a struct field because the safe answer is
// true, and a struct's zero value would quietly make every call foreign.
func ResolveSessionHandle(args map[string]any, hook func() (string, error), projectID string, sessionParamIsOurs bool) SessionResolution {
	// Hook mode is checked first, before ownership. A hook-mode server injects
	// session_id into nothing, so sessionParamIsOurs is false for every tool;
	// testing ownership first would turn every hook-mode call foreign.
	if hook != nil {
		id, err := safeHook(hook)
		trimmed := strings.TrimSpace(id)
		if err == nil && trimmed != "" {
			return SessionResolution{DeriveSessionID(trimmed, projectID), SessionSourceHook, true}
		}
		if err != nil {
			logger.Warnf("ResolveSessionID hook failed; minting a session silently: %v", err)
		}
		return SessionResolution{MintSessionID(), SessionSourceMinted, true}
	}
	if !sessionParamIsOurs {
		// The tool declares its own session_id. Nothing in the arguments is
		// ours to read, and the customer's value is not ours to adopt.
		return SessionResolution{Source: SessionSourceForeign}
	}
	if raw, ok := ExtractHandle(args, constants.ParamSessionID); ok {
		// Trim before validating: an agent that echoes the handle back with
		// stray whitespace still means the ID it was given, and the
		// TypeScript SDK trims here too.
		supplied := strings.TrimSpace(raw)
		if IsValidSessionID(supplied) {
			return SessionResolution{SessionID: supplied, Source: SessionSourceSupplied}
		}
		// Publish sessionless rather than adopt it. The raw value is never
		// stored on the resolution — it still appears in the recorded request
		// arguments, where redaction can reach it.
		return SessionResolution{Source: SessionSourceInvalid}
	}
	return SessionResolution{SessionID: MintSessionID(), Source: SessionSourceMinted}
}

func safeHook(hook func() (string, error)) (id string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ResolveSessionID hook panicked: %v", r)
		}
	}()
	return hook()
}

// maxAgentIDBytes matches validation.MaxTagValueLength: the SDK tag channel
// bypasses customer tag validation, so it clamps to the same budget itself.
const maxAgentIDBytes = 200

// ClampAgentID prepares a supplied agent_id for the SDK tag channel, which
// bypasses customer tag validation: CR/LF become spaces and the value is
// truncated to 200 bytes. Truncation always lands on a rune boundary — a tag
// carrying half a UTF-8 sequence is not valid UTF-8 and would not survive
// JSON encoding intact. The un-clamped value still appears in the recorded
// raw request.
func ClampAgentID(v string) string {
	v = strings.ReplaceAll(v, "\r", " ")
	v = strings.ReplaceAll(v, "\n", " ")
	if len(v) <= maxAgentIDBytes {
		return v
	}
	v = v[:maxAgentIDBytes]
	// A cut mid-sequence leaves an incomplete rune at the tail; DecodeLastRune
	// reports that as (RuneError, 1), which a genuine U+FFFD (3 bytes) never
	// matches. Drop bytes until the tail decodes.
	for len(v) > 0 {
		if r, size := utf8.DecodeLastRuneInString(v); r == utf8.RuneError && size <= 1 {
			v = v[:len(v)-1]
			continue
		}
		break
	}
	return v
}

// BuildMintBackText renders the trailing [MCP INSTRUCTIONS] text block for one
// call, or "" when there is nothing to say.
//
// Taking the whole resolution rather than an ID is deliberate: this is the one
// place that decides whether a call announces anything at all. Both adapters
// used to re-derive that condition themselves, which is exactly how the two of
// them drift apart.
//
//	minted  — announce the new handle.
//	invalid — correct the agent, without issuing a replacement.
//	hook    — nothing: no parameter exists for the agent to echo.
//	foreign — nothing: the parameter is not ours to speak about.
//	supplied— nothing new to say; the mirror confirms it instead.
func BuildMintBackText(res SessionResolution) string {
	if res.HookMode {
		return ""
	}
	switch res.Source {
	case SessionSourceMinted:
		return constants.MintBackHeaderSession + "\n" +
			constants.MintBackSessionLine(res.SessionID) + "\n" +
			constants.MintBackCloser
	case SessionSourceInvalid:
		return constants.MintBackHeaderInvalid + "\n" +
			constants.MintBackInvalidLine + "\n" +
			constants.MintBackCloser
	default:
		return ""
	}
}
