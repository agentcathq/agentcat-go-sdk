package core

import (
	"fmt"
	"strings"

	agentcatapi "go.agentcat.com/api"
)

// UserIdentity represents a user identity returned by the identify function
type UserIdentity struct {
	// UserID is the unique identifier for the user
	UserID string `json:"userId"`

	// UserName is the optional user name
	UserName string `json:"userName,omitempty"`

	// UserData contains additional user data as key-value pairs
	UserData map[string]any `json:"userData,omitempty"`
}

// RedactFunc redacts sensitive information from text before it is sent upstream
type RedactFunc func(text string) string

// Exporter is an interface for telemetry exporters that forward events
// to external systems.
type Exporter interface {
	Export(event *Event) error
}

// ExporterConfig configures a telemetry exporter. Type selects the exporter
// implementation; the remaining fields apply only to the exporter noted in
// their comment. Unknown types are skipped with a warning logged.
type ExporterConfig struct {
	// Type selects the exporter: "otlp", "datadog", "sentry", or "posthog".
	Type string `json:"type"`

	// Endpoint is the OTLP collector endpoint (otlp, required). The /v1/traces
	// path is appended automatically when missing.
	Endpoint string `json:"endpoint,omitempty"`

	// Headers are additional HTTP headers sent with each request (otlp).
	Headers map[string]string `json:"headers,omitempty"`

	// APIKey authenticates with the provider (datadog and posthog, required).
	APIKey string `json:"apiKey,omitempty"`

	// Site is the Datadog site, e.g. "datadoghq.com" or "datadoghq.eu"
	// (datadog, required).
	Site string `json:"site,omitempty"`

	// Service is the service name reported to Datadog (datadog, required).
	Service string `json:"service,omitempty"`

	// Env is the deployment environment tag (datadog, optional).
	Env string `json:"env,omitempty"`

	// DSN is the Sentry DSN (sentry, required).
	DSN string `json:"dsn,omitempty"`

	// Environment is the Sentry environment tag (sentry, optional).
	Environment string `json:"environment,omitempty"`

	// Release is the Sentry release tag (sentry, optional).
	Release string `json:"release,omitempty"`

	// EnableTracing also sends Sentry transactions for performance monitoring
	// (sentry; default false — logs and error events only).
	EnableTracing bool `json:"enableTracing,omitempty"`

	// Host is the PostHog instance URL (posthog; default
	// https://us.i.posthog.com, supports self-hosted and EU region).
	Host string `json:"host,omitempty"`

	// EnableAITracing emits $ai_span events for tool calls alongside regular
	// capture events, integrating with PostHog's AI observability views
	// (posthog; default false).
	EnableAITracing bool `json:"enableAITracing,omitempty"`
}

// Options configures the AgentCat tracking behavior.
type Options struct {
	// DisableReportMissing, when true, prevents the automatic "get_more_tools"
	// tool from being registered. By default (false) the tool is added so LLMs
	// can report missing functionality.
	DisableReportMissing bool

	// DisableToolCallContext, when true, prevents the "context" parameter from
	// being injected into existing tools. By default (false) the parameter is
	// added to capture user intent.
	DisableToolCallContext bool

	// DisableTracing, when true, prevents any events from being published to
	// the AgentCat API. Context parameter injection and the get_more_tools tool
	// still honor their own flags.
	DisableTracing bool

	// CustomContextDescription overrides the default description of the
	// injected "context" parameter. Only applies when tool call context
	// injection is enabled.
	CustomContextDescription string

	// Debug enables debug logging to ~/agentcat.log. When false, no logging occurs.
	Debug bool

	// RedactSensitiveInformation redacts sensitive data before sending to AgentCat.
	RedactSensitiveInformation RedactFunc

	// Exporters configure telemetry exporters to send events to external systems.
	// Available exporters: otlp, datadog, sentry, posthog.
	Exporters map[string]ExporterConfig

	// DisableDiagnostics disables AgentCat's internal SDK diagnostics (anonymous
	// operational error/setup reporting used to detect SDK setup failures).
	// On by default; also disable via the DISABLE_DIAGNOSTICS env var.
	// Local ~/agentcat.log logging is unaffected.
	DisableDiagnostics bool

	// APIBaseURL overrides the default AgentCat API endpoint.
	// When empty, the SDK falls back to the AGENTCAT_API_URL environment
	// variable, then the legacy MCPCAT_API_URL environment variable, and then
	// to the built-in default (https://api.agentcat.com).
	APIBaseURL string
}

// Event wraps the API publish request and leaves room for SDK-specific fields.
type Event struct {
	agentcatapi.PublishEventRequest
	// SDK-specific fields can be added here if needed in the future. They should
	// use `json:"-"` to avoid serialization to the API.
}

// IDPrefix represents prefixes for AgentCat-generated IDs.
type IDPrefix string

const (
	// PrefixSession is the prefix for session IDs.
	PrefixSession IDPrefix = "ses"

	// PrefixEvent is the prefix for event IDs.
	PrefixEvent IDPrefix = "evt"
)

// Session represents session-level metadata that can be attached to events.
type Session struct {
	// Session ID uniquely identifies this session
	SessionID *string `json:"session_id,omitempty"`

	// Project ID for AgentCat tracking
	ProjectID *string `json:"project_id,omitempty"`

	// IP address of the client
	IpAddress *string `json:"ip_address,omitempty"`

	// Programming language of the SDK used
	SdkLanguage *string `json:"sdk_language,omitempty"`

	// Version of AgentCat being used
	AgentcatVersion *string `json:"agentcat_version,omitempty"`

	// Name of the MCP server
	ServerName *string `json:"server_name,omitempty"`

	// Version of the MCP server
	ServerVersion *string `json:"server_version,omitempty"`

	// Name of the MCP client
	ClientName *string `json:"client_name,omitempty"`

	// Version of the MCP client
	ClientVersion *string `json:"client_version,omitempty"`

	// Actor ID for agentcat:identify events
	IdentifyActorGivenId *string `json:"identify_actor_given_id,omitempty"`

	// Actor name for agentcat:identify events
	IdentifyActorName *string `json:"identify_actor_name,omitempty"`

	// Additional data for agentcat:identify events
	IdentifyData map[string]any `json:"identify_data,omitempty"`
}

// String returns a formatted string representation of the Session
func (s *Session) String() string {
	if s == nil {
		return "Session: <nil>"
	}

	// Helper to safely dereference string pointers
	deref := func(p *string) string {
		if p != nil {
			return *p
		}
		return "<not set>"
	}

	var b strings.Builder
	b.WriteString("Session {\n")
	if s.ProjectID != nil {
		b.WriteString("  Project: " + *s.ProjectID + "\n")
	}
	b.WriteString("  Client: " + deref(s.ClientName))
	if s.ClientVersion != nil {
		b.WriteString(" v" + *s.ClientVersion)
	}
	b.WriteString("\n")

	b.WriteString("  Server: " + deref(s.ServerName))
	if s.ServerVersion != nil {
		b.WriteString(" v" + *s.ServerVersion)
	}
	b.WriteString("\n")

	b.WriteString("  SDK: " + deref(s.SdkLanguage))
	if s.AgentcatVersion != nil {
		b.WriteString(" (AgentCat v" + *s.AgentcatVersion + ")")
	}
	b.WriteString("\n")

	if s.IpAddress != nil {
		b.WriteString("  IP: " + *s.IpAddress + "\n")
	}

	if s.IdentifyActorGivenId != nil || s.IdentifyActorName != nil {
		b.WriteString("  Identity: ")
		if s.IdentifyActorGivenId != nil {
			b.WriteString("ID=" + *s.IdentifyActorGivenId)
		}
		if s.IdentifyActorName != nil {
			if s.IdentifyActorGivenId != nil {
				b.WriteString(", ")
			}
			b.WriteString("Name=" + *s.IdentifyActorName)
		}
		b.WriteString("\n")
	}

	if len(s.IdentifyData) > 0 {
		b.WriteString("  Additional Data: ")
		first := true
		for k, v := range s.IdentifyData {
			if !first {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s=%v", k, v)
			first = false
		}
		b.WriteString("\n")
	}

	b.WriteString("}")
	return b.String()
}

// AgentCatInstance represents the tracking configuration stored in the registry.
type AgentCatInstance struct {
	ProjectID string
	Options   *Options
	ServerRef any

	// SessionID is a server-level session ID generated at Track() time.
	// It is used for custom events published against the tracked server.
	SessionID string
}

// MCPcatInstance is the former name of AgentCatInstance.
//
// Deprecated: use AgentCatInstance.
type MCPcatInstance = AgentCatInstance

// CustomEventData describes a customer-defined event published via
// PublishCustomEvent.
type CustomEventData struct {
	// ResourceName names the resource or action this event represents.
	ResourceName string

	// Parameters are arbitrary request data to attach to the event.
	Parameters map[string]any

	// Response is arbitrary response data to attach to the event.
	Response map[string]any

	// Message describes why the event occurred (sent as user_intent).
	Message string

	// Duration of the operation in milliseconds.
	Duration *int32

	// IsError marks the event as an error event.
	IsError bool

	// Error holds error details when IsError is true.
	Error error

	// Tags are custom string key-value pairs, validated client-side
	// (same constraints as the EventTags callback).
	Tags map[string]string

	// Properties are arbitrary JSON metadata attached to the event.
	Properties map[string]any
}

// DefaultOptions returns the default options for tracking.
// All features are enabled by default (Disable* fields are false).
func DefaultOptions() Options {
	return Options{}
}

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T {
	return &v
}
