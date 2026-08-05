package agentcat

import (
	"errors"
	"reflect"
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/v2/internal/core"
	"go.agentcat.com/sdk/v2/internal/event"
	"go.agentcat.com/sdk/v2/internal/exceptions"
	"go.agentcat.com/sdk/v2/internal/logging"
	"go.agentcat.com/sdk/v2/internal/publisher"
	"go.agentcat.com/sdk/v2/internal/registry"
	"go.agentcat.com/sdk/v2/internal/validation"
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
// previously passed to an adapter's Track function) or a session ID string.
// A string is used verbatim as the event's session ID — no derivation or
// validation is applied — so events correlate with whatever session or
// correlation ID the caller already holds. For a tracked server, the event
// publishes without a session unless one is provided via data.SessionID.
// A non-empty data.SessionID always takes precedence over a string target.
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
		// v2: the string is the session ID, used VERBATIM — no derivation.
		sessionID = target
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
		// v2: tracked-server custom events publish without a session unless
		// one is explicitly provided via CustomEventData.SessionID.
		sessionID = ""
	}

	if data == nil {
		data = &CustomEventData{}
	}
	if data.SessionID != "" {
		sessionID = data.SessionID // explicit SessionID always wins
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
	// Explicit null rather than an omitted key, so a sessionless custom event
	// and a sessionless tool-call event look identical on the wire.
	if sessionID != "" {
		evt.SetSessionId(sessionID)
	} else {
		evt.SetSessionIdNil()
	}

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
	evt.AgentcatVersion = core.Ptr(core.GetDependencyVersion(core.SDKModulePath))

	// Publish through the global publisher, initializing it if needed.
	// For tracked servers, reuse the server's redaction, API base URL, and
	// exporter configuration.
	var redactFn RedactFunc
	var redactEventFn RedactEventFunc
	var exporterConfigs map[string]ExporterConfig
	apiBaseURL := ResolveAPIBaseURL("")
	if instance != nil && instance.Options != nil {
		redactFn = instance.Options.RedactSensitiveInformation
		redactEventFn = instance.Options.RedactEvent
		exporterConfigs = instance.Options.Exporters
		if instance.Options.APIBaseURL != "" {
			apiBaseURL = instance.Options.APIBaseURL
		}
	}

	pub := publisher.GetOrInit(redactFn, redactEventFn, apiBaseURL, exporterConfigs)
	pub.Publish(evt)

	logging.New().Debugf("Published custom event (session %q) with type %q", sessionID, CustomEventType)

	return nil
}
