package core

import (
	"fmt"

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

// RedactEventFunc is the event-level redaction hook. It receives the full
// event before it is published and returns a modified event, or nil to drop
// the event entirely. A non-nil error also drops the event and is logged.
type RedactEventFunc func(event *Event) (*Event, error)

// Exporter is an interface for telemetry exporters that forward events
// to external systems.
type Exporter interface {
	Export(event Event) error
}

// ExporterConfig configures a telemetry exporter. Additional configuration keys
// depend on the selected exporter implementation.
type ExporterConfig struct {
	Type   string         `json:"type"`
	Config map[string]any `json:"config,omitempty"`
}

// Options configures the MCPCat tracking behavior.
type Options struct {
	// DisableReportMissing, when true, prevents the automatic "get_more_tools"
	// tool from being registered. By default (false) the tool is added so LLMs
	// can report missing functionality.
	DisableReportMissing bool

	// DisableToolCallContext, when true, prevents the "context" parameter from
	// being injected into existing tools. By default (false) the parameter is
	// added to capture user intent.
	DisableToolCallContext bool

	// Debug enables debug logging to ~/mcpcat.log. When false, no logging occurs.
	Debug bool

	// RedactSensitiveInformation redacts sensitive data before sending to MCPCat.
	RedactSensitiveInformation RedactFunc

	// RedactEvent is the event-level redaction hook, invoked with the full
	// event (inspect ResourceName, EventType, Parameters, Response, etc.)
	// before it is published. Return a modified event, or nil to drop the
	// event entirely. Runs before RedactSensitiveInformation, so it sees
	// raw, unredacted values. The system-managed fields Id, SessionId,
	// ProjectId, EventType, and Timestamp cannot be changed. If the hook
	// returns an error or panics, the event is dropped and the error is
	// logged.
	RedactEvent RedactEventFunc

	// Exporters configure telemetry exporters to send events to external systems.
	// Available exporters: otlp, datadog, sentry (TODO: implement in future).
	Exporters map[string]ExporterConfig

	// DisableDiagnostics disables MCPCat's internal SDK diagnostics (anonymous
	// operational error/setup reporting used to detect SDK setup failures).
	// On by default; also disable via the DISABLE_DIAGNOSTICS env var.
	// Local ~/mcpcat.log logging is unaffected.
	DisableDiagnostics bool

	// APIBaseURL overrides the default MCPCat API endpoint.
	// When empty, the SDK falls back to the MCPCAT_API_URL environment variable,
	// and then to the built-in default (https://api.mcpcat.io).
	APIBaseURL string
}

// Event wraps the API publish request and leaves room for SDK-specific fields.
type Event struct {
	agentcatapi.PublishEventRequest
	// SDK-specific fields can be added here if needed in the future. They should
	// use `json:"-"` to avoid serialization to the API.
}

// IDPrefix represents prefixes for MCPCat-generated IDs.
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

	// Project ID for MCPCat tracking
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

	// Actor ID for mcpcat:identify events
	IdentifyActorGivenId *string `json:"identify_actor_given_id,omitempty"`

	// Actor name for mcpcat:identify events
	IdentifyActorName *string `json:"identify_actor_name,omitempty"`

	// Additional data for mcpcat:identify events
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

	result := "Session {\n"
	if s.ProjectID != nil {
		result += "  Project: " + *s.ProjectID + "\n"
	}
	result += "  Client: " + deref(s.ClientName)
	if s.ClientVersion != nil {
		result += " v" + *s.ClientVersion
	}
	result += "\n"

	result += "  Server: " + deref(s.ServerName)
	if s.ServerVersion != nil {
		result += " v" + *s.ServerVersion
	}
	result += "\n"

	result += "  SDK: " + deref(s.SdkLanguage)
	if s.AgentcatVersion != nil {
		result += " (AgentCat v" + *s.AgentcatVersion + ")"
	}
	result += "\n"

	if s.IpAddress != nil {
		result += "  IP: " + *s.IpAddress + "\n"
	}

	if s.IdentifyActorGivenId != nil || s.IdentifyActorName != nil {
		result += "  Identity: "
		if s.IdentifyActorGivenId != nil {
			result += "ID=" + *s.IdentifyActorGivenId
		}
		if s.IdentifyActorName != nil {
			if s.IdentifyActorGivenId != nil {
				result += ", "
			}
			result += "Name=" + *s.IdentifyActorName
		}
		result += "\n"
	}

	if len(s.IdentifyData) > 0 {
		result += "  Additional Data: "
		first := true
		for k, v := range s.IdentifyData {
			if !first {
				result += ", "
			}
			result += k + "=" + fmt.Sprintf("%v", v)
			first = false
		}
		result += "\n"
	}

	result += "}"
	return result
}

// MCPcatInstance represents the tracking configuration stored in the registry.
type MCPcatInstance struct {
	ProjectID string
	Options   *Options
	ServerRef any
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
