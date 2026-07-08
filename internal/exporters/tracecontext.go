// Package exporters implements telemetry exporters that forward AgentCat
// events to external observability systems (OTLP collectors, Datadog, Sentry,
// PostHog), mirroring the TypeScript SDK's exporter modules. Exporters are
// fail-open: they run fire-and-forget in parallel with the AgentCat API send,
// and failures are logged, never propagated to the customer's server.
package exporters

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"
)

// sourceValue identifies AgentCat as the origin of exported telemetry,
// matching the TypeScript SDK's AGENTCAT_SOURCE constant.
const sourceValue = "agentcat"

// httpClient is shared by all exporters; a bounded timeout keeps a slow
// telemetry backend from pinning goroutines forever.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// TraceID returns a deterministic 32-hex-char trace ID derived from the
// session ID (the first 16 bytes of its SHA-256 hash). When sessionID is
// empty, a random trace ID is returned.
func TraceID(sessionID string) string {
	if sessionID == "" {
		return randomHex(16)
	}
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])[:32]
}

// SpanID returns a deterministic 16-hex-char span ID derived from the event
// ID (the first 8 bytes of its SHA-256 hash). When eventID is empty, a random
// span ID is returned.
func SpanID(eventID string) string {
	if eventID == "" {
		return randomHex(8)
	}
	sum := sha256.Sum256([]byte(eventID))
	return hex.EncodeToString(sum[:])[:16]
}

// DatadogTraceID returns the trace ID as an unsigned 64-bit decimal string
// (Datadog's native trace ID format), derived from the low 64 bits of the
// deterministic trace ID.
func DatadogTraceID(sessionID string) string {
	h := TraceID(sessionID)
	return hexToDecimal(h[16:32])
}

// DatadogSpanID returns the span ID as an unsigned 64-bit decimal string.
func DatadogSpanID(eventID string) string {
	return hexToDecimal(SpanID(eventID))
}

func hexToDecimal(h string) string {
	v, err := strconv.ParseUint(h, 16, 64)
	if err != nil {
		return "0"
	}
	return strconv.FormatUint(v, 10)
}

// randomHex returns n random bytes hex-encoded (2n hex chars).
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand read failures are effectively impossible; fall back to
		// zeros rather than propagating an error into the export path.
		for i := range buf {
			buf[i] = 0
		}
	}
	return hex.EncodeToString(buf)
}
