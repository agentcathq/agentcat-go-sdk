// Package constants holds AgentCat's agent-facing copy and wire keys.
// Every string is byte-identical to the TypeScript SDK
// (src/modules/constants.ts); fleet-wide consistency of the prompt surface
// is the point. Do not reword without updating the cross-SDK copy spec.
package constants

// Injected parameter names and wire keys.
const (
	ParamSessionID = "session_id"
	ParamAgentID   = "agent_id"
	ParamContext   = "context"

	// MCPSessionKey is the structuredContent member carrying the handle
	// mirror, and the property declared on extended output schemas. Its
	// members are named by ParamSessionID, ParamAgentID, and the status enum
	// owned by the inject package.
	MCPSessionKey = "mcp_session"

	MetaClientInfoKey      = "io.modelcontextprotocol/clientInfo"
	MetaProtocolVersionKey = "io.modelcontextprotocol/protocolVersion"
)

// SDK-owned event tags.
const (
	TagSessionSource   = "agentcat_session_id_source"
	TagAgentID         = "agentcat_agent_id"
	TagAgentSource     = "agentcat_agent_id_source"
	TagProtocolVersion = "agentcat_protocol_version"
	TagMRTR            = "agentcat_mrtr"

	MRTRInputRequired = "input_required"
	MRTRContinuation  = "continuation"
)

// ID prefixes. PrefixAgent is reserved: server-side agent minting was
// removed during design, but the prefix must never be reused.
const (
	PrefixSession = "ses"
	PrefixEvent   = "evt"
	PrefixAgent   = "agt"
)

const SessionIDParamDescription = "Session continuity handle, one of two values: the ses_ ID issued for the task underway, or start. This server cannot link your calls between requests on its own, so session continuity travels in this parameter instead. If you were handed a session_id for this task — for example by the agent that spawned you — send that exact value from your first call. Otherwise send start on your first call; the server will issue an opaque correlation ID in the mcp_session field of the result, or in a text block at the start of the result beginning [session_id issued. Then send that exact ses_ value on every later call and hand it to any subagents working the same task. start always begins a new, unrelated task — never send it mid-task. If you send a value this server does not recognize, the result reports it: mcp_session.status of unrecognized, or a text block beginning [session_id unrecognized; re-send the ID issued for this task, or start if none was issued yet. Never invent a ses_ value."

// SessionIDParamPattern is the JSON Schema pattern declared on the injected
// session_id parameter. Its ses_ arm is exactly the shape both issuing paths
// produce (KSUID: 27 base62 chars) — the same check handles.IsValidSessionID
// applies — with the start sentinel as the only other member.
const SessionIDParamPattern = "^(start|ses_[0-9A-Za-z]{27})$"

// SessionStartSentinel is the session_id value that requests issuance: it
// resolves exactly like an omitted session_id (matched case-insensitively;
// the schema documents lowercase).
const SessionStartSentinel = "start"

// AgentIDParamDescription is the single agent_id description, used in both
// prompted and hook mode.
const AgentIDParamDescription = "Agent identity handle, required on every call including your first. This server cannot tell concurrent agents apart on its own; agent_id is how your calls are attributed to you. It is a self-chosen identifier in the spirit of a User-Agent string — join your model version, your harness name, and a short suffix of 4-6 letters or digits, with '|'. Example: opus-4.80-1m|claude-code|k3n9x. Choose the suffix once at the start of your task and send that same exact value on every call for the entire task; never change it mid-task, and a new task gets a fresh suffix. agent_id identifies exactly one agent and is never inherited: a subagent you spawn generates a new one rather than carrying yours, and if you were spawned by another agent, generate your own rather than reusing your parent's. A call without agent_id cannot be attributed to you."

// Mint-back text block parts. The minted block is
// MintBackHeaderIssued + "\n" + MintBackSessionLine(id) + "\n" + MintBackIssuedBody;
// the unrecognized block is
// MintBackHeaderUnrecognized + "\n" + MintBackUnrecognizedBody.
const (
	MintBackHeaderIssued       = "[session_id issued — see this tool's session_id parameter description]"
	MintBackHeaderUnrecognized = "[session_id unrecognized — see this tool's session_id parameter description]"
)

const MintBackIssuedBody = "This is the first-call issuance described in this tool's session_id parameter description."

// MintBackUnrecognizedBody names no session ID: no replacement is issued in
// the same response. If a value arrived at all the agent was already issued a
// good one, and handing it a second would split a conversation that was never
// split; the recovery path is described in the body itself.
const MintBackUnrecognizedBody = "The value sent was not issued by this server. Re-send the session_id issued earlier for this task; if none was issued yet, send start and one will be issued."

// MintBackSessionLine renders the session_id line of the minted text block.
func MintBackSessionLine(sessionID string) string {
	return "session_id: " + sessionID
}

// mcp_session outputSchema copy: the field description per mode, and the
// per-property descriptions. The status enum values live in the inject
// package, which declares the enum alongside MCPSessionStatusDescription.
const MCPSessionFieldDescription = "Session continuity and agent attribution state for this task, returned on completed responses that carry structured output. This server cannot link your calls between requests on its own, so session continuity travels here instead."

const MCPSessionFieldDescriptionHookMode = "Agent attribution state for this task, returned on completed responses that carry structured output."

const MCPSessionSessionIDDescription = "Opaque correlation ID for this task, issued by this server. Use this as the session_id argument of every later call, and hand it to any subagents working the same task. Absent when status is unrecognized; no replacement is issued in that response — recovery is described under status."

const MCPSessionAgentIDDescription = "Present only when you sent agent_id on this call. Your agent_id, echoed as received. Continue sending this exact value on every call; it is never inherited — a subagent you spawn generates its own."

const MCPSessionStatusDescription = "issued: first call of a task; the session_id above was just created. active: the session_id you sent was accepted; keep sending it. unrecognized: the value sent was not issued by this server — re-send the one issued earlier for this task; if none was issued yet, send start to be issued a new one."

// get_more_tools copy (unchanged from v1; pinned here as the single source).
const (
	GetMoreToolsName               = "get_more_tools"
	GetMoreToolsDescription        = "Check for additional tools whenever your task might benefit from specialized capabilities - even if existing tools could work as a fallback."
	GetMoreToolsContextDescription = "A description of your goal and what kind of tool would help accomplish it."
	GetMoreToolsResponseText       = "Unfortunately, we have shown you the full tool list. We have noted your feedback and will work to improve the tool list in the future."
)
