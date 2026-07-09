package agentcat

import (
	"errors"
	"reflect"
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/event"
	"go.agentcat.com/sdk/internal/exceptions"
	"go.agentcat.com/sdk/internal/logging"
	"go.agentcat.com/sdk/internal/publisher"
	"go.agentcat.com/sdk/internal/registry"
	"go.agentcat.com/sdk/internal/session"
	"go.agentcat.com/sdk/internal/validation"
)

// CustomEventType is the wire event type for customer-published custom events.
const CustomEventType = "agentcat:custom"

// Sentinel errors for PublishCustomEvent validation.
var (
	ErrServerNotTracked = errors.New("agentcat: server is not tracked; call Track first or provide a session ID string")
	ErrInvalidTarget    = errors.New("agentcat: first parameter must be either an MCP server or a session ID string")
)

// PublishCustomEvent publishes a customer-defined event to AgentCat.
//
// serverOrSessionID is either a tracked MCP server instance (any server
// previously passed to an adapter's Track function) or an MCP session ID
// string. For a tracked server, the event uses the server's session; for a
// session ID string, a deterministic session ID is derived from it so events
// correlate with automatically captured events for that transport session.
//
// projectID is required. data is optional event payload.
func PublishCustomEvent(serverOrSessionID any, projectID string, data *CustomEventData) error {
	if projectID == "" {
		return ErrEmptyProjectID
	}

	var (
		sessionID string
		instance  *AgentCatInstance
	)

	switch target := serverOrSessionID.(type) {
	case string:
		// Custom session ID provided: derive a deterministic session ID.
		sessionID = session.DeriveSessionIDFromMCPSession(target, projectID)
	case nil:
		return ErrInvalidTarget
	default:
		if reflect.ValueOf(serverOrSessionID).Kind() != reflect.Ptr {
			return ErrInvalidTarget
		}
		instance = registry.Get(serverOrSessionID)
		if instance == nil {
			return ErrServerNotTracked
		}
		if instance.Options != nil && instance.Options.DisableTracing {
			// Tracing disabled: accept and drop, matching auto-capture behavior.
			return nil
		}
		sessionID = instance.SessionID
	}

	if data == nil {
		data = &CustomEventData{}
	}

	eventID := event.NewEventID()
	eventType := CustomEventType

	evt := &Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Id:        &eventID,
			ProjectId: projectID,
			EventType: &eventType,
			Timestamp: core.Ptr(time.Now()),
			Duration:  data.Duration,
		},
	}
	evt.SetSessionId(sessionID)

	if data.ResourceName != "" {
		evt.ResourceName = &data.ResourceName
	}
	evt.Parameters = data.Parameters
	evt.Response = data.Response
	if data.Message != "" {
		evt.UserIntent = &data.Message
	}
	if data.IsError {
		evt.IsError = core.Ptr(true)
		if data.Error != nil {
			evt.Error = exceptions.CaptureException(data.Error)
		}
	}

	// Customer-defined metadata: tags are validated, properties pass through.
	if tags := validation.ValidateTags(data.Tags); tags != nil {
		evt.Tags = &tags
	}
	if len(data.Properties) > 0 {
		evt.Properties = data.Properties
	}

	evt.SdkLanguage = core.Ptr("Go")
	evt.AgentcatVersion = core.Ptr(session.GetDependencyVersion("go.agentcat.com/sdk"))

	// Publish through the global publisher, initializing it if needed.
	// For tracked servers, reuse the server's redaction, API base URL, and
	// exporter configuration.
	var redactFn RedactFunc
	var exporterConfigs map[string]ExporterConfig
	apiBaseURL := ResolveAPIBaseURL("")
	if instance != nil && instance.Options != nil {
		redactFn = instance.Options.RedactSensitiveInformation
		exporterConfigs = instance.Options.Exporters
		if instance.Options.APIBaseURL != "" {
			apiBaseURL = instance.Options.APIBaseURL
		}
	}

	pub := publisher.GetOrInit(redactFn, apiBaseURL, exporterConfigs)
	pub.Publish(evt)

	logging.New().Debugf("Published custom event for session %s with type %q", sessionID, CustomEventType)

	return nil
}
