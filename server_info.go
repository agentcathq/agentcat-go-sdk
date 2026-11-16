package agentcat

import (
	"context"
	"time"

	"go.agentcat.com/sdk/v2/internal/logging"
	"go.agentcat.com/sdk/v2/internal/serverinfo"
)

// Server info heartbeat (integration API for the adapter modules). About once
// an hour per process and server name, a handled request starts a heartbeat
// that calls the server's own list handlers and posts what an agent sees on
// the server to AgentCat. The request path only checks a small map of times;
// the list calls, the redaction and the POST run on a goroutine that recovers
// every panic.

type (
	// ServerInfoTrigger names the kind of request that runs a heartbeat check.
	ServerInfoTrigger = serverinfo.Trigger

	// ServerInfoSource is what an adapter knows about one server: the fields
	// its library keeps and a handler for each list.
	ServerInfoSource = serverinfo.Source

	// ServerInfoPage is one page of a list.
	ServerInfoPage = serverinfo.Page

	// ServerInfoListFunc calls the server's handler for one page of a list.
	ServerInfoListFunc = serverinfo.ListFunc

	// ServerInfoRequest is the body of one heartbeat.
	ServerInfoRequest = serverinfo.Request
)

const (
	// ServerInfoTriggerToolsList is a tools/list request.
	ServerInfoTriggerToolsList = serverinfo.TriggerToolsList
	// ServerInfoTriggerFeedback is a call of the get_more_tools tool.
	ServerInfoTriggerFeedback = serverinfo.TriggerFeedback
	// ServerInfoTriggerToolCall is any other tools/call request.
	ServerInfoTriggerToolCall = serverinfo.TriggerToolCall
)

// The keys of the lists in a heartbeat's features.
const (
	ServerInfoKeyTools             = serverinfo.KeyTools
	ServerInfoKeyResources         = serverinfo.KeyResources
	ServerInfoKeyResourceTemplates = serverinfo.KeyResourceTemplates
	ServerInfoKeyPrompts           = serverinfo.KeyPrompts
)

// ServerInfoTriggerForTool returns the trigger of a tools/call request for
// the named tool: a call of get_more_tools is a feedback note.
func ServerInfoTriggerForTool(toolName string) ServerInfoTrigger {
	if toolName == GetMoreToolsName {
		return ServerInfoTriggerFeedback
	}
	return ServerInfoTriggerToolCall
}

// MaybeStartServerInfo runs the heartbeat check for one handled request and,
// when a heartbeat is due, starts it on a new goroutine. It reads memory only
// and never blocks; load runs on the heartbeat's goroutine. A request that a
// heartbeat itself made never starts another one.
func MaybeStartServerInfo(ctx context.Context, trigger ServerInfoTrigger, instance *AgentCatInstance, serverName string, load func() (*ServerInfoSource, error)) {
	defer func() {
		if r := recover(); r != nil {
			logging.New().Debugf("server info: recovered panic in the check: %v", r)
		}
	}()
	if instance == nil || !serverinfo.Enabled() || serverinfo.IsHeartbeat(ctx) {
		return
	}
	tracing := instance.Options != nil && !instance.Options.DisableTracing
	if !serverinfo.Check(trigger, instance.ProjectID, serverName, tracing) {
		return
	}
	var redact func(string) string
	if instance.Options != nil {
		redact = instance.Options.RedactSensitiveInformation
	}
	serverinfo.Start(ctx, serverinfo.Target{
		ProjectID:  instance.ProjectID,
		ServerName: serverName,
		Redact:     redact,
	}, load)
}

// SetServerInfoSenderForTest replaces the heartbeat's sender and returns a
// function that restores it. For tests: the test setup of each adapter
// installs a recorder so that no test sends a heartbeat to the network.
func SetServerInfoSenderForTest(send func(ctx context.Context, body *ServerInfoRequest) (status int, err error)) (restore func()) {
	return serverinfo.SetSender(send)
}

// SetServerInfoEnabledForTest turns the heartbeat on or off for the whole
// process and returns a function that restores it. For tests: a suite whose
// fixtures do not expect the heartbeat's list calls into their hooks and
// handlers turns it off, and the heartbeat's own tests turn it back on.
func SetServerInfoEnabledForTest(enabled bool) (restore func()) {
	return serverinfo.SetEnabled(enabled)
}

// SetServerInfoClockForTest replaces the clock of the heartbeat check and
// returns a function that restores it. For tests.
func SetServerInfoClockForTest(now func() time.Time) (restore func()) {
	return serverinfo.SetClock(now)
}

// ResetServerInfoForTest empties the heartbeat's memory, as a new process
// would start. For tests.
func ResetServerInfoForTest() {
	serverinfo.ResetMemory()
}

// WaitServerInfoForTest waits until no heartbeat is running, or until
// timeout, and reports whether every heartbeat finished. For tests.
func WaitServerInfoForTest(timeout time.Duration) bool {
	return serverinfo.Wait(timeout)
}
