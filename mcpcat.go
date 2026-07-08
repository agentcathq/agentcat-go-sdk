package agentcat

import (
	"context"
	"errors"
	"os"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/diagnostics"
	"go.agentcat.com/sdk/internal/event"
	"go.agentcat.com/sdk/internal/logging"
	"go.agentcat.com/sdk/internal/publisher"
	"go.agentcat.com/sdk/internal/redaction"
	"go.agentcat.com/sdk/internal/registry"
	"go.agentcat.com/sdk/internal/session"
	"go.agentcat.com/sdk/internal/validation"
)

// Sentinel errors for Track validation.
var (
	ErrNilServer      = errors.New("agentcat: server must not be nil")
	ErrEmptyProjectID = errors.New("agentcat: projectID must not be empty")
)

// --- Integration API (used by mcpgo/ and officialsdk/ modules) ---

// RegisterServer stores the AgentCat instance for a given server in the global registry.
func RegisterServer(server any, instance *AgentCatInstance) {
	registry.Register(server, instance)
}

// GetInstance retrieves the AgentCat instance for a given server from the global registry.
func GetInstance(server any) *AgentCatInstance {
	return registry.Get(server)
}

// UnregisterServer removes a server from the global registry.
func UnregisterServer(server any) {
	registry.Unregister(server)
}

// InitPublisher initializes the global event publisher and returns a publish function.
// The returned function can be called to publish events asynchronously.
// If apiBaseURL is empty, the default AgentCat API URL is used. When exporter
// configs are provided, every published event is also fanned out to the
// configured telemetry exporters, independently of the AgentCat API send.
func InitPublisher(redactFn RedactFunc, apiBaseURL string, exporterConfigs map[string]ExporterConfig) func(evt *Event) {
	pub := publisher.GetOrInit(redactFn, apiBaseURL, exporterConfigs)
	return func(evt *Event) {
		if evt != nil {
			pub.Publish(evt)
		}
	}
}

// Shutdown gracefully shuts down the global event publisher.
// This should be called when the application is shutting down to ensure
// all queued events are published before exit. The provided context controls
// the shutdown deadline; if no deadline is set, a default 5-second timeout
// is applied.
func Shutdown(ctx context.Context) error {
	err := publisher.ShutdownGlobal(ctx)
	diagnostics.Flush()
	return err
}

// SetDebug enables or disables debug logging globally.
func SetDebug(debug bool) {
	logging.SetGlobalDebug(debug)
}

// LogRecoveredPanic logs a panic recovered inside SDK capture code (hooks,
// middleware, capture goroutines). Integration API for adapter modules:
// analytics failures must never crash the customer's server, so capture code
// recovers, calls this, and drops the event.
func LogRecoveredPanic(where string, recovered any) {
	logging.New().Errorf("Recovered panic in %s: %v - event dropped", where, recovered)
}

// NewEvent creates an SDK-agnostic event from session data and basic metadata.
func NewEvent(sess *Session, eventType string, duration *int32, isError bool, errorDetails error) *Event {
	return event.NewEvent(sess, eventType, duration, isError, errorDetails)
}

// NewSessionID generates a new unique session ID with the AgentCat prefix.
func NewSessionID() string {
	return session.GenerateSessionID()
}

// DeriveSessionID derives a deterministic session ID from an MCP transport
// session ID and optional project ID. The same inputs always produce the same
// session ID, so sessions remain stable across server restarts. The
// derivation matches the TypeScript SDK's deriveSessionIdFromMCPSession.
func DeriveSessionID(mcpSessionID, projectID string) string {
	return session.DeriveSessionIDFromMCPSession(mcpSessionID, projectID)
}

// ValidateTags validates customer-supplied event tags against AgentCat's
// client-side constraints, dropping (and warn-logging) invalid entries.
// Returns nil when no valid entries remain.
func ValidateTags(tags map[string]string) map[string]string {
	return validation.ValidateTags(tags)
}

// IdentitiesEqual reports whether two user identities are deeply equal.
func IdentitiesEqual(a, b *UserIdentity) bool {
	return core.IdentitiesEqual(a, b)
}

// MergeIdentities merges a new identity into a previous one: UserID and
// UserName are overwritten while UserData fields are merged (next wins).
func MergeIdentities(previous, next *UserIdentity) *UserIdentity {
	return core.MergeIdentities(previous, next)
}

// GetDependencyVersion returns the version of the given module from build info,
// or "dev" if the module is not found.
func GetDependencyVersion(modulePath string) string {
	return session.GetDependencyVersion(modulePath)
}

// NewEventID generates a new unique event ID with the AgentCat prefix.
func NewEventID() string {
	return event.NewEventID()
}

// CreateIdentifyEvent creates an Event for agentcat:identify event type.
func CreateIdentifyEvent(sess *Session) *Event {
	return event.CreateIdentifyEvent(sess)
}

// RedactEvent applies the redaction function to sensitive fields in the event.
func RedactEvent(evt *Event, redactFn RedactFunc) error {
	return redaction.RedactEvent(evt, redactFn)
}

// ConvertToMap converts any value to map[string]any or []any via JSON round-trip.
func ConvertToMap(v any) any {
	return event.ConvertToMap(v)
}

// ResolveAPIBaseURL returns the API base URL to use, applying the priority:
// code option > AGENTCAT_API_URL env var > MCPCAT_API_URL env var (legacy
// fallback) > empty string (publisher uses default).
func ResolveAPIBaseURL(optionURL string) string {
	if optionURL != "" {
		return optionURL
	}
	if envURL := os.Getenv("AGENTCAT_API_URL"); envURL != "" {
		return envURL
	}
	if envURL := os.Getenv("MCPCAT_API_URL"); envURL != "" {
		return envURL
	}
	return ""
}

// Ptr returns a pointer to the given value. Convenience helper for integration modules.
func Ptr[T any](v T) *T {
	return core.Ptr(v)
}
