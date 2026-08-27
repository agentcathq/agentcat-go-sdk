package constants

import "testing"

func TestAgentFacingCopyIsPinned(t *testing.T) {
	// Byte-copies from agentcat-typescript-sdk src/modules/constants.ts
	// (src/modules/tools.ts for the get_more_tools copy).
	pins := map[string]struct{ got, want string }{
		"session_id description":        {SessionIDParamDescription, "Session continuity handle, one of two values: the ses_ ID issued for the task underway, or start. This server cannot link your calls between requests on its own, so session continuity travels in this parameter instead. If you were handed a session_id for this task — for example by the agent that spawned you — send that exact value from your first call. Otherwise send start on your first call; the server will issue an opaque correlation ID in the mcp_session field of the result, or in a text block at the start of the result beginning [session_id issued. Then send that exact ses_ value on every later call and hand it to any subagents working the same task. start always begins a new, unrelated task — never send it mid-task. If you send a value this server does not recognize, the result reports it: mcp_session.status of unrecognized, or a text block beginning [session_id unrecognized; re-send the ID issued for this task, or start if none was issued yet. Never invent a ses_ value."},
		"session_id pattern":            {SessionIDParamPattern, "^(start|ses_[0-9A-Za-z]{27})$"},
		"session start sentinel":        {SessionStartSentinel, "start"},
		"agent_id description":          {AgentIDParamDescription, "Agent identity handle, required on every call including your first. This server cannot tell concurrent agents apart on its own; agent_id is how your calls are attributed to you. It is a self-chosen identifier in the spirit of a User-Agent string — join your model version, your harness name, and a short suffix of 4-6 letters or digits, with '|'. Example: opus-4.80-1m|claude-code|k3n9x. Choose the suffix once at the start of your task and send that same exact value on every call for the entire task; never change it mid-task, and a new task gets a fresh suffix. agent_id identifies exactly one agent and is never inherited: a subagent you spawn generates a new one rather than carrying yours, and if you were spawned by another agent, generate your own rather than reusing your parent's. A call without agent_id cannot be attributed to you."},
		"mint-back issued header":       {MintBackHeaderIssued, "[session_id issued — see this tool's session_id parameter description]"},
		"mint-back issued body":         {MintBackIssuedBody, "This is the first-call issuance described in this tool's session_id parameter description."},
		"mint-back unrecognized header": {MintBackHeaderUnrecognized, "[session_id unrecognized — see this tool's session_id parameter description]"},
		"mint-back unrecognized body":   {MintBackUnrecognizedBody, "The value sent was not issued by this server. Re-send the session_id issued earlier for this task; if none was issued yet, send start and one will be issued."},
		"mint-back session line":        {MintBackSessionLine("ses_2cOHEO0LYGADMzRvWTXXVbbgxgm"), "session_id: ses_2cOHEO0LYGADMzRvWTXXVbbgxgm"},
		"field description":             {MCPSessionFieldDescription, "Session continuity and agent attribution state for this task, returned on completed responses that carry structured output. This server cannot link your calls between requests on its own, so session continuity travels here instead."},
		"field description hook mode":   {MCPSessionFieldDescriptionHookMode, "Agent attribution state for this task, returned on completed responses that carry structured output."},
		"session_id sub-property":       {MCPSessionSessionIDDescription, "Opaque correlation ID for this task, issued by this server. Use this as the session_id argument of every later call, and hand it to any subagents working the same task. Absent when status is unrecognized; no replacement is issued in that response — recovery is described under status."},
		"agent_id sub-property":         {MCPSessionAgentIDDescription, "Present only when you sent agent_id on this call. Your agent_id, echoed as received. Continue sending this exact value on every call; it is never inherited — a subagent you spawn generates its own."},
		"status sub-property":           {MCPSessionStatusDescription, "issued: first call of a task; the session_id above was just created. active: the session_id you sent was accepted; keep sending it. unrecognized: the value sent was not issued by this server — re-send the one issued earlier for this task; if none was issued yet, send start to be issued a new one."},
		"param session_id":              {ParamSessionID, "session_id"},
		"param agent_id":                {ParamAgentID, "agent_id"},
		"param context":                 {ParamContext, "context"},
		"mcp session key":               {MCPSessionKey, "mcp_session"},
		"tag session source":            {TagSessionSource, "agentcat_session_id_source"},
		"tag agent id":                  {TagAgentID, "agentcat_agent_id"},
		"tag agent source":              {TagAgentSource, "agentcat_agent_id_source"},
		"tag protocol version":          {TagProtocolVersion, "agentcat_protocol_version"},
		"tag mrtr":                      {TagMRTR, "agentcat_mrtr"},
		"mrtr input required":           {MRTRInputRequired, "input_required"},
		"mrtr continuation":             {MRTRContinuation, "continuation"},
		"prefix session":                {PrefixSession, "ses"},
		"prefix event":                  {PrefixEvent, "evt"},
		"prefix agent":                  {PrefixAgent, "agt"},
		"meta client info":              {MetaClientInfoKey, "io.modelcontextprotocol/clientInfo"},
		"meta protocol version":         {MetaProtocolVersionKey, "io.modelcontextprotocol/protocolVersion"},
		"get_more_tools name":           {GetMoreToolsName, "get_more_tools"},
		"get_more_tools description":    {GetMoreToolsDescription, "Check for additional tools whenever your task might benefit from specialized capabilities - even if existing tools could work as a fallback."},
		"get_more_tools context":        {GetMoreToolsContextDescription, "A description of your goal and what kind of tool would help accomplish it."},
		"get_more_tools response":       {GetMoreToolsResponseText, "Unfortunately, we have shown you the full tool list. We have noted your feedback and will work to improve the tool list in the future."},
	}
	for name, p := range pins {
		if p.got != p.want {
			t.Errorf("%s drifted:\n got:  %q\n want: %q", name, p.got, p.want)
		}
	}
}
