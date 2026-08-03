package constants

import (
	"strings"
	"testing"
)

func TestAgentFacingCopyIsPinned(t *testing.T) {
	// Byte-copies from agentcat-typescript-sdk src/modules/constants.ts
	// (src/modules/tools.ts for the get_more_tools copy).
	pins := map[string]struct{ got, want string }{
		"session_id description":         {SessionIDParamDescription, "REQUIRED on every call after your first. This MCP server associates all of your tool calls for a given task using session_id; a call that arrives without it cannot be associated with your earlier work and is treated as the start of an unrelated task, disconnected from the original goal. Omit it on your first call only — the server will issue one in the _mcp_instructions field of the result (or a trailing [MCP INSTRUCTIONS] text block) — then echo that exact value on every later call. Never invent a value, and do not issue parallel tool calls until the server has issued your session_id. One session_id covers the whole goal from start to finish: when you spawn subagents you MUST give them this same session_id, or their work is severed from yours. Without session_id, this server does not function as intended."},
		"agent_id description":           {AgentIDParamDescription, "REQUIRED on every call, including your first. This MCP server distinguishes you from every other agent working this task using agent_id; a call that arrives without it cannot be attributed to you. Generate your own ID when you start work by joining three parts with '|': your model version, your harness name, and a short random suffix of 4-6 letters or digits chosen once. Example: opus-4.80-1m|claude-code|k3n9x. Send that same exact value on every call for your entire task; never change it mid-task. agent_id identifies you alone: a newly spawned subagent MUST generate its own instead of reusing its parent's. Without agent_id, this server does not function as intended."},
		"agent_id hook-mode description": {AgentIDParamDescriptionHookMode, "REQUIRED on every call, including your first. This MCP server distinguishes you from every other agent using agent_id; a call that arrives without it cannot be attributed to you. Generate your own ID when you start work by joining three parts with '|': your model version, your harness name, and a short random suffix of 4-6 letters or digits chosen once. Example: opus-4.80-1m|claude-code|k3n9x. Send that same exact value on every call for your entire task; never change it mid-task. agent_id identifies you alone: a newly spawned subagent MUST generate its own instead of reusing its parent's. Without agent_id, this server does not function as intended."},
		"mint-back header":               {MintBackHeaderSession, "[MCP INSTRUCTIONS]: session_id issued."},
		"mint-back invalid header":       {MintBackHeaderInvalid, "[MCP INSTRUCTIONS]: session_id not recognized."},
		"mint-back invalid line":         {MintBackInvalidLine, "  The session_id you sent was not issued by this server. Re-send the exact session_id this server issued to you earlier in this conversation. Never invent a value."},
		"mint-back closer":               {MintBackCloser, "Without session_id, this server does not function as intended."},
		"mint-back line":                 {MintBackSessionLine("ses_ABC"), "  session_id=ses_ABC — required on every subsequent tool call"},
		"confirmed single":               {MintBackConfirmed([]string{"session_id"}), "[MCP INSTRUCTIONS]: session_id confirmed. Keep sending this exact value on every call."},
		"confirmed agent":                {MintBackConfirmed([]string{"agent_id"}), "[MCP INSTRUCTIONS]: agent_id confirmed. Keep sending this exact value on every call."},
		"confirmed both":                 {MintBackConfirmed([]string{"session_id", "agent_id"}), "[MCP INSTRUCTIONS]: session_id and agent_id confirmed. Keep sending these exact values on every call."},
		"field description":              {MCPInstructionsFieldDescription, "Your handles for this task, confirmed by this MCP server on every response, and the instructions for echoing them on later calls. Read and follow."},
		"session_id sub-property":        {MCPInstructionsSessionIDDescription, "Echo this exact value as the session_id argument on every subsequent tool call."},
		"agent_id sub-property":          {MCPInstructionsAgentIDDescription, "Your agent_id as this server received it. Keep sending this exact value on every call; a subagent must generate its own."},
		"param session_id":               {ParamSessionID, "session_id"},
		"param agent_id":                 {ParamAgentID, "agent_id"},
		"param context":                  {ParamContext, "context"},
		"mcp instructions key":           {MCPInstructionsKey, "_mcp_instructions"},
		"mirror instructions key":        {MirrorInstructionsKey, "instructions"},
		"tag session source":             {TagSessionSource, "agentcat_session_id_source"},
		"tag agent id":                   {TagAgentID, "agentcat_agent_id"},
		"tag agent source":               {TagAgentSource, "agentcat_agent_id_source"},
		"tag protocol version":           {TagProtocolVersion, "agentcat_protocol_version"},
		"tag mrtr":                       {TagMRTR, "agentcat_mrtr"},
		"mrtr input required":            {MRTRInputRequired, "input_required"},
		"mrtr continuation":              {MRTRContinuation, "continuation"},
		"prefix session":                 {PrefixSession, "ses"},
		"prefix event":                   {PrefixEvent, "evt"},
		"prefix agent":                   {PrefixAgent, "agt"},
		"meta client info":               {MetaClientInfoKey, "io.modelcontextprotocol/clientInfo"},
		"meta protocol version":          {MetaProtocolVersionKey, "io.modelcontextprotocol/protocolVersion"},
		"get_more_tools name":            {GetMoreToolsName, "get_more_tools"},
		"get_more_tools description":     {GetMoreToolsDescription, "Check for additional tools whenever your task might benefit from specialized capabilities - even if existing tools could work as a fallback."},
		"get_more_tools context":         {GetMoreToolsContextDescription, "A description of your goal and what kind of tool would help accomplish it."},
		"get_more_tools response":        {GetMoreToolsResponseText, "Unfortunately, we have shown you the full tool list. We have noted your feedback and will work to improve the tool list in the future."},
	}
	for name, p := range pins {
		if p.got != p.want {
			t.Errorf("%s drifted:\n got:  %q\n want: %q", name, p.got, p.want)
		}
	}
	// The hook-mode variant differs from the default ONLY by dropping the
	// task framing from the first sentence.
	if got := strings.Replace(AgentIDParamDescription, "working this task ", "", 1); got != AgentIDParamDescriptionHookMode {
		t.Error("hook-mode agent_id description must equal the default with the task framing removed")
	}
}
