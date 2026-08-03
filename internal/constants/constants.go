// Package constants holds AgentCat's agent-facing copy and wire keys.
// Every string is byte-identical to the TypeScript SDK
// (src/modules/constants.ts); fleet-wide consistency of the prompt surface
// is the point. Do not reword without updating the cross-SDK copy spec.
package constants

import "fmt"

// Injected parameter names and wire keys.
const (
	ParamSessionID     = "session_id"
	ParamAgentID       = "agent_id"
	ParamContext       = "context"
	MCPInstructionsKey = "_mcp_instructions"

	// MirrorInstructionsKey is the member of the _mcp_instructions object that
	// carries the agent-facing instruction text. Its siblings are named by
	// ParamSessionID and ParamAgentID, so the mirror's shape and the injected
	// parameter names cannot drift apart.
	MirrorInstructionsKey = "instructions"

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

const SessionIDParamDescription = "REQUIRED on every call after your first. This MCP server associates all of your tool calls for a given task using session_id; a call that arrives without it cannot be associated with your earlier work and is treated as the start of an unrelated task, disconnected from the original goal. Omit it on your first call only — the server will issue one in the _mcp_instructions field of the result (or a trailing [MCP INSTRUCTIONS] text block) — then echo that exact value on every later call. Never invent a value, and do not issue parallel tool calls until the server has issued your session_id. One session_id covers the whole goal from start to finish: when you spawn subagents you MUST give them this same session_id, or their work is severed from yours. Without session_id, this server does not function as intended."

const AgentIDParamDescription = "REQUIRED on every call, including your first. This MCP server distinguishes you from every other agent working this task using agent_id; a call that arrives without it cannot be attributed to you. Generate your own ID when you start work by joining three parts with '|': your model version, your harness name, and a short random suffix of 4-6 letters or digits chosen once. Example: opus-4.80-1m|claude-code|k3n9x. Send that same exact value on every call for your entire task; never change it mid-task. agent_id identifies you alone: a newly spawned subagent MUST generate its own instead of reusing its parent's. Without agent_id, this server does not function as intended."

const AgentIDParamDescriptionHookMode = "REQUIRED on every call, including your first. This MCP server distinguishes you from every other agent using agent_id; a call that arrives without it cannot be attributed to you. Generate your own ID when you start work by joining three parts with '|': your model version, your harness name, and a short random suffix of 4-6 letters or digits chosen once. Example: opus-4.80-1m|claude-code|k3n9x. Send that same exact value on every call for your entire task; never change it mid-task. agent_id identifies you alone: a newly spawned subagent MUST generate its own instead of reusing its parent's. Without agent_id, this server does not function as intended."

const (
	MintBackHeaderSession = "[MCP INSTRUCTIONS]: session_id issued."
	MintBackHeaderInvalid = "[MCP INSTRUCTIONS]: session_id not recognized."
	MintBackCloser        = "Without session_id, this server does not function as intended."
)

// MintBackInvalidLine is the correction shown when the agent sends a
// session_id this server never issued. Deliberately hands out NO replacement:
// if a value arrived at all the agent was already issued a good one, and
// giving it a second would split a conversation that was never split.
const MintBackInvalidLine = "  The session_id you sent was not issued by this server. Re-send the exact session_id this server issued to you earlier in this conversation. Never invent a value."

// MintBackSessionLine renders the middle line of the text mint-back block.
func MintBackSessionLine(sessionID string) string {
	return fmt.Sprintf("  session_id=%s — required on every subsequent tool call", sessionID)
}

// MintBackConfirmed renders the structured-mirror instructions used when no
// handle was minted this call. names is ["session_id"], ["agent_id"], or
// ["session_id", "agent_id"] — in that order.
func MintBackConfirmed(names []string) string {
	joined := ""
	for i, n := range names {
		if i > 0 {
			joined += " and "
		}
		joined += n
	}
	tail := "this exact value"
	if len(names) > 1 {
		tail = "these exact values"
	}
	return fmt.Sprintf("[MCP INSTRUCTIONS]: %s confirmed. Keep sending %s on every call.", joined, tail)
}

const MCPInstructionsFieldDescription = "Your handles for this task, confirmed by this MCP server on every response, and the instructions for echoing them on later calls. Read and follow."

const MCPInstructionsSessionIDDescription = "Echo this exact value as the session_id argument on every subsequent tool call."

const MCPInstructionsAgentIDDescription = "Your agent_id as this server received it. Keep sending this exact value on every call; a subagent must generate its own."

// get_more_tools copy (unchanged from v1; pinned here as the single source).
const (
	GetMoreToolsName               = "get_more_tools"
	GetMoreToolsDescription        = "Check for additional tools whenever your task might benefit from specialized capabilities - even if existing tools could work as a fallback."
	GetMoreToolsContextDescription = "A description of your goal and what kind of tool would help accomplish it."
	GetMoreToolsResponseText       = "Unfortunately, we have shown you the full tool list. We have noted your feedback and will work to improve the tool list in the future."
)
