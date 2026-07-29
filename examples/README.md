# AgentCat Go SDK Examples

These examples show how to integrate AgentCat into MCP servers built with the two most popular Go MCP libraries.

Each example is a standalone echo server with three tools (`echo`, `reverse`, `count_chars`) that runs over Streamable HTTP.

## Examples

| Example | Port | Description |
|---------|------|-------------|
| [mcpgo/basic](mcpgo/basic) | 8081 | Minimal 3-line AgentCat integration with [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) |
| [mcpgo/advanced](mcpgo/advanced) | 8082 | Full AgentCat options (per-event Identify hook, Redact, Debug) with mark3labs/mcp-go |
| [officialsdk/basic](officialsdk/basic) | 8083 | Minimal 3-line AgentCat integration with the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) |
| [officialsdk/advanced](officialsdk/advanced) | 8084 | Full AgentCat options (per-event Identify hook, Redact, Debug) with the official Go MCP SDK |

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
shutdown, err := mcpcat.Track(server, "proj_YOUR_PROJECT_ID", nil)
if err != nil { log.Fatal(err) }
defer shutdown(context.Background())
```

All tool calls, resource reads, and protocol events are captured automatically.

### Advanced

The advanced examples show all available AgentCat options:

- **Identify** — attach user identity (ID, name, metadata) to each session; the callback runs on every auto-captured event and receives the triggering MCP request (the examples type-assert `*mcp.CallToolRequest` to identify on tool calls only)
- **RedactSensitiveInformation** — strip sensitive data (e.g. emails) before it leaves the process
- **Debug** — enable debug logging to `~/mcpcat.log`
- **DisableToolCallContext** — opt out of the injected `context` parameter (enabled by default)
- **DisableReportMissing** — opt out of the `get_more_tools` tool (enabled by default)

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

- Go 1.24+
- A AgentCat project ID from [mcpcat.io](https://mcpcat.io) — set via `MCPCAT_PROJECT_ID` env var or edit the fallback in the code
