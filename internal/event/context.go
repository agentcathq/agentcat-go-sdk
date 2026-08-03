package event

import (
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/v2/internal/constants"
	"go.agentcat.com/sdk/v2/internal/core"
	"go.agentcat.com/sdk/v2/internal/exceptions"
	"go.agentcat.com/sdk/v2/internal/handles"
)

// EventContext carries everything resolved per request that gets stamped
// onto the published event. There is no cache and no state shared between
// requests: adapters build a fresh EventContext for every call.
type EventContext struct {
	ProjectID       string
	SessionID       string
	SessionSource   string
	ServerName      string
	ServerVersion   string
	ClientName      string
	ClientVersion   string
	ProtocolVersion string
	AgentID         string // supplied verbatim; "" when absent
	MRTR            string // "", "input_required", or "continuation"
	SDKLanguage     string
	AgentcatVersion string
	Identity        *core.UserIdentity // per-call identify result; nil when absent
}

// NewToolCallEvent builds the single mcp:tools/call event for one call.
func NewToolCallEvent(ec *EventContext, duration *int32, isError bool, errorDetails error) *Event {
	eventID := NewEventID()
	eventType := "mcp:tools/call"
	evt := &Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Id:        &eventID,
			ProjectId: ec.ProjectID,
			EventType: &eventType,
			Duration:  duration,
			Timestamp: core.Ptr(time.Now()),
		},
	}
	// A sessionless event carries an explicit JSON null, never "".
	// SetSessionId("") marks the NullableString as SET, so the API would
	// receive an empty-string session and treat it as a real handle that every
	// other sessionless event shares. Matches the TypeScript SDK's
	// `sessionId: event.sessionId || null`.
	if ec.SessionID != "" {
		evt.SetSessionId(ec.SessionID)
	} else {
		evt.SetSessionIdNil()
	}

	setIf := func(dst **string, v string) {
		if v != "" {
			*dst = core.Ptr(v)
		}
	}
	setIf(&evt.ServerName, ec.ServerName)
	setIf(&evt.ServerVersion, ec.ServerVersion)
	setIf(&evt.ClientName, ec.ClientName)
	setIf(&evt.ClientVersion, ec.ClientVersion)
	setIf(&evt.SdkLanguage, ec.SDKLanguage)
	setIf(&evt.AgentcatVersion, ec.AgentcatVersion)

	if ec.Identity != nil {
		evt.IdentifyActorGivenId = core.Ptr(ec.Identity.UserID)
		if ec.Identity.UserName != "" {
			evt.IdentifyActorName = core.Ptr(ec.Identity.UserName)
		}
		evt.IdentifyData = ec.Identity.UserData
	}

	if isError {
		evt.IsError = core.Ptr(true)
		if errorDetails != nil {
			evt.Error = exceptions.CaptureException(errorDetails)
		}
	}
	return evt
}

// ApplySDKTags merges the SDK-owned tags into the event AFTER customer tags
// (SDK wins on collision). SDK tags bypass customer tag validation and the
// 50-tag cap, so agent IDs are clamped for this channel.
func ApplySDKTags(evt *Event, ec *EventContext) {
	tags := map[string]string{}
	if evt.Tags != nil {
		for k, v := range *evt.Tags {
			tags[k] = v
		}
	}
	tags[constants.TagSessionSource] = ec.SessionSource
	if ec.AgentID != "" {
		tags[constants.TagAgentID] = handles.ClampAgentID(ec.AgentID)
		tags[constants.TagAgentSource] = "supplied"
	}
	if ec.ProtocolVersion != "" {
		tags[constants.TagProtocolVersion] = ec.ProtocolVersion
	}
	if ec.MRTR != "" {
		tags[constants.TagMRTR] = ec.MRTR
	}
	evt.Tags = &tags
}
