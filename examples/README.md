# AgentCat Go SDK Examples

These examples show how to integrate AgentCat into MCP servers built with the two most popular Go MCP libraries.

Each example is a standalone echo server that runs over Streamable HTTP.

## Examples

| Example | Port | Description |
|---------|------|-------------|
| [mcpgo/basic](mcpgo/basic) | 8081 | Minimal 3-line AgentCat integration with [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) |
| [mcpgo/advanced](mcpgo/advanced) | 8082 | Full AgentCat v2 options (per-call `Identify`, `EnableAgentTracking`, hook mode, hook composition, redaction, debug) with mark3labs/mcp-go |
| [officialsdk/basic](officialsdk/basic) | 8083 | Minimal 3-line AgentCat integration with the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) |
| [officialsdk/advanced](officialsdk/advanced) | 8084 | Full AgentCat v2 options (per-call `Identify`, `EnableAgentTracking`, hook mode, redaction, debug) with the official Go MCP SDK |
| [officialsdk/factory](officialsdk/factory) | 8080 | Stateless per-request server factory — `Track()` inside `getServer`, the expected 2026 deployment shape |

## Running an Example

Each example is its own Go module. To run one:

```bash
cd examples/mcpgo/basic
go run .
```

The server starts on its configured port (see table above) and accepts Streamable HTTP connections at `/mcp`. To use it with an MCP client, point the client at the URL. For instance, in a Claude Desktop `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "echo": {
      "url": "http://localhost:8081/mcp"
    }
  }
}
```

### Claude Code

Add the server to your Claude Code configuration by running:

```bash
claude mcp add echo-server http://localhost:8081/mcp
```

## What the Examples Demonstrate

### Basic

The basic examples show that AgentCat integration is just 3 lines of code added to a normal MCP server:

```go
shutdown, err := agentcat.Track(s, "proj_YOUR_PROJECT_ID", nil)
if err != nil { log.Fatal(err) }
defer shutdown(context.Background())
```

Every tool call is captured automatically. MCP 2026-07-28 has no protocol sessions, so AgentCat correlates the calls belonging to one task through an explicit `session_id` parameter it adds to each tool schema, mints back to the agent on its first call, and strips out again before your handler runs.

### Advanced

The advanced examples show the v2 options:

- **`Identify`** — attach user identity (ID, name, metadata) to a call's event. It runs on **every tool call**, uncached, and stamps only that event; there is no session to merge into and no separate identify event, so keep it cheap and make no network calls in it
- **`EnableAgentTracking`** — also inject a required `agent_id` parameter so parallel agents on one task can be told apart (off by default)
- **`ResolveSessionID` (hook mode)** — shown in a comment block: return your own correlation ID and AgentCat derives the session from it deterministically. In hook mode no `session_id` is injected anywhere and no session instructions are shown to the agent

AgentCat trusts only a `session_id` it issued (`ses_` plus a 27-character KSUID). Anything else publishes without a session and the agent is told to re-send the real one — so a tool that declares its own `session_id` parameter cannot be correlated. Use `ResolveSessionID` if you already manage sessions yourself.
- **`RedactSensitiveInformation`** — strip sensitive data (e.g. emails) before it leaves the process
- **`Debug`** — enable debug logging to `~/agentcat.log`
- **`DisableToolCallContext`** — shown in a comment block: opt out of the injected `context` parameter (enabled by default)
- **`DisableReportMissing`** — shown in a comment block: opt out of the `get_more_tools` tool (enabled by default)

The mcp-go example additionally shows hook composition: register your own hooks with `server.WithHooks` at construction and AgentCat appends its own to them (v1's `Options.Hooks` field is gone).

### Factory

`officialsdk/factory` is the stateless deployment shape: `mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true})` builds a fresh `mcp.Server` per HTTP request and calls `Track()` on it inside the factory. It demonstrates that:

- module-level state (publisher, logger, diagnostics) initializes once no matter how many servers you track, and per-server registry entries are released when the server becomes unreachable — a per-request factory does not leak;
- correlation survives the hop, because the `session_id` handle travels on the wire rather than in server memory;
- **rebuild on demand** works: a stateless client can send `tools/call` to an instance that never served a `tools/list`, and AgentCat rebuilds its injection registries from that server's own tool list on the first call, so arguments are still stripped and the mint-back still reaches the agent;
- shutdown is process-wide: the per-server function `Track()` returns is ignored in the factory, and `agentcat.Shutdown(ctx)` drains the queue once at exit.

## Configuration

All examples read the project ID from the `MCPCAT_PROJECT_ID` environment variable. If the variable is not set, they fall back to `"proj_YOUR_PROJECT_ID"`.

```bash
export MCPCAT_PROJECT_ID="proj_abc123"
cd examples/mcpgo/basic
go run .
```

You can also pass it inline when configuring an MCP client:

```json
{
  "mcpServers": {
    "echo": {
      "command": "go",
      "args": ["run", "."],
      "cwd": "/path/to/agentcat-go-sdk/examples/mcpgo/basic",
      "env": {
        "MCPCAT_PROJECT_ID": "proj_abc123"
      }
    }
  }
}
```

## Prerequisites

- Go 1.25+
- An AgentCat project ID from [agentcat.com](https://agentcat.com) — set via `MCPCAT_PROJECT_ID` env var or edit the fallback in the code
