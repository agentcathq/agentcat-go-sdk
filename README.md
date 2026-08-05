<div align="center">
  <img alt="AgentCat — see exactly how agents experience your product" src="docs/static/og-image.png" width="80%">
</div>
<h3 align="center">
    <a href="#getting-started">Getting Started</a>
    <span> · </span>
    <a href="#why-use-agentcat-">Features</a>
    <span> · </span>
    <a href="https://docs.agentcat.com">Docs</a>
    <span> · </span>
    <a href="https://agentcat.com">Website</a>
    <span> · </span>
    <a href="#free-for-open-source">Open Source</a>
    <span> · </span>
    <a href="https://meet.agentcat.com/meet">Schedule a Demo</a>
</h3>
<p align="center">
  <a href="https://pkg.go.dev/go.agentcat.com/sdk/v2"><img src="https://pkg.go.dev/badge/go.agentcat.com/sdk/v2.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/go.agentcat.com/sdk/v2"><img src="https://goreportcard.com/badge/go.agentcat.com/sdk/v2" alt="Go Report Card"></a>
  <a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go" alt="Go Version"></a>
  <a href="https://github.com/agentcathq/agentcat-go-sdk/issues"><img src="https://img.shields.io/github/issues/agentcathq/agentcat-go-sdk.svg" alt="GitHub issues"></a>
  <a href="https://github.com/agentcathq/agentcat-go-sdk/actions"><img src="https://github.com/agentcathq/agentcat-go-sdk/workflows/CI/badge.svg" alt="CI"></a>
</p>

> [!NOTE]
> AgentCat v2 introduces compatibility with the [MCP Protocol "Stateless" 2026-07-28 Update](https://blog.modelcontextprotocol.io/posts/2026-07-28/) and the coinciding [mcp-go](https://github.com/mark3labs/mcp-go) and official [go-sdk](https://github.com/modelcontextprotocol/go-sdk) releases that put it into effect. The stateless transition has a massive impact on analytics, as sessions were a built-in concept tying related tool calls together. AgentCat has now migrated its session tracking under guidance of the MCP core team's recommendations of using [explicit handles (SEP-2567)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2567).
>
> As a result AgentCat now injects a `session_id` on every MCP tool call to associate them under the same task umbrella. Our evals show much higher tool correlation accuracy at the cost of < 1% additional context pollution.

> [!IMPORTANT]
> **MCPcat is now AgentCat** 🐱 — same team, same product, new name. This module was previously published as [`mcpcat-go-sdk`](https://github.com/mcpcat/mcpcat-go-sdk), which keeps working forever, but new features land here. Upgrading takes a few minutes — see the [migration guide](./MIGRATION.md).

AgentCat is an analytics platform for MCP server owners 🐱. It captures user intentions and behavior patterns to help you understand what AI users actually need from your tools — eliminating guesswork and accelerating product development all with one-line of code.

This SDK also provides a free and simple way to forward telemetry like logs, traces, and errors to any Open Telemetry collector or popular tools like Datadog, Sentry, and PostHog.

```bash
# mark3labs/mcp-go (v0.53.0 – v0.57.0)
go get go.agentcat.com/sdk/mcpgo/v2

# official modelcontextprotocol/go-sdk (v1.4.1 – v1.7.0)
go get go.agentcat.com/sdk/officialsdk/v2
```

To learn more about us, check us out [here](https://agentcat.com). For detailed guides visit our [documentation](https://docs.agentcat.com).

## Why use AgentCat? 🤔

AgentCat helps builders of MCP servers, Claude Connectors, and ChatGPT Plugins learn how to improve them by capturing any agents goals and detecting when they get stuck.

Use AgentCat for:

- **Agent session replay** 🎬. Follow alongside your users and their agents to understand why they're using your MCP servers, what functionality you're missing, and what clients they're coming from.
- **Trace debugging** 🔍. See where your users are getting stuck, track and find when LLMs get confused by your API, and debug sessions across all deployments of your MCP server.
- **Existing platform support** 📊. Get logging and tracing out of the box for your existing observability platforms (OpenTelemetry, Datadog, Sentry) — eliminating the tedious work of implementing telemetry yourself.

<img alt="AgentCat architecture — the AgentCat SDK inside your MCP server sends analytics to your observability vendors and session replay to the AgentCat dashboard" src="docs/static/architecture.png" />

## How it works

AgentCat works as a lightweight middleware inside your MCP server. When you call `Track()`, it seamlessly modifies your registered tool schemas in place, following the MCP core team's [explicit handles (SEP-2567)](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2567) guidelines. Concretely, AgentCat adds the following to your server:

- **`session_id`** — a parameter injected into each tool's input schema. Agents echo it back on every call, letting AgentCat group related tool calls into one task even over stateless transports. Values are validated: anything AgentCat did not issue is rejected rather than adopted, and the agent is told to re-send the ID it was given.
- **`agent_id`** _(off by default)_ — enabled with `EnableAgentTracking: true`. Each agent self-generates its own ID, keeping parallel agents working the same task individually attributable.
- **`context`** — a parameter asking the agent to explain, in one sentence, why it is making this call. This is where intent data comes from.
- **`get_more_tools`** — an additional tool, prompt-engineered so that agents readily report the features and tools they looked for but couldn't find — surfacing your missing functionality directly from real usage.

Injected parameters are stripped from arguments before your tool handler runs, so your code never sees them. For tools that declare an output schema, issued IDs are also mirrored into `structuredContent` (as `_mcp_instructions`), so clients that only read structured results still receive them.

## Getting Started

To get started with AgentCat, first create an account and obtain your project ID by signing up at [agentcat.com](https://agentcat.com). For detailed setup instructions visit our [documentation](https://docs.agentcat.com).

Once you have your project ID, integrate AgentCat into your MCP server:

**mark3labs/mcp-go:**

```go
import agentcat "go.agentcat.com/sdk/mcpgo/v2"

// Track the server with AgentCat
shutdown, err := agentcat.Track(mcpServer, "proj_0000000", nil)
if err != nil { log.Fatal(err) } // on error shutdown is nil — do not defer it
defer shutdown(context.Background()) // flushes queued events before exit
```

**Official go-sdk:**

```go
import agentcat "go.agentcat.com/sdk/officialsdk/v2"

// Track the server with AgentCat
shutdown, err := agentcat.Track(mcpServer, "proj_0000000", nil)
if err != nil { log.Fatal(err) } // on error shutdown is nil — do not defer it
defer shutdown(context.Background()) // flushes queued events before exit
```

Stateless servers built on [MCP 2026-07-28](https://blog.modelcontextprotocol.io/posts/2026-07-28/) create a fresh server instance per request (`mcp.NewStreamableHTTPHandler`) or per connection. Call `Track()` inside the factory so every instance is tracked:

```go
// A complete runnable program is in examples/officialsdk/factory.
handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
    s := newServer()
    if _, err := agentcat.Track(s, "proj_0000000", nil); err != nil {
        log.Printf("agentcat: %v", err) // never fail a request over analytics
    }
    return s // skip the per-server shutdown here; drain once at exit with agentcat.Shutdown(ctx)
}, &mcp.StreamableHTTPOptions{Stateless: true})
```

Calling `Track()` per instance is cheap — the event queue, telemetry exporters, and diagnostics are initialized once and shared across instances.

### Identifying users

We strongly encourage identifying every actor. If you can't resolve a real user, return a stable anonymized ID instead — for example, a hash of the auth token or API key — so that all events from the same end user still roll up to one actor in your dashboard rather than scattering into anonymous one-off sessions.

`Identify` runs on every tool call, uncached, and stamps only that one event. Because it is on the hot path of every call, keep it cheap: read from the context, headers, or an already-parsed token, and make no network calls. Return `nil` (or an identity with an empty `UserID`) to skip identification for a call.

The callback receives the raw MCP request — in both adapters the value passed is the `*mcp.CallToolRequest` that triggered the event:

**mark3labs/mcp-go:**

```go
import (
    "github.com/mark3labs/mcp-go/mcp"
    agentcat "go.agentcat.com/sdk/mcpgo/v2"
)

shutdown, err := agentcat.Track(mcpServer, "proj_0000000", &agentcat.Options{
    Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
        req := request.(*mcp.CallToolRequest) // always a tool call in v2
        _ = req // extract identity from the request, ctx, headers, or an auth token
        return &agentcat.UserIdentity{
            UserID: "user_12345", UserName: "demo_user",
            UserData: map[string]any{"email": "demo@example.com"},
        }
    },
})
```

**Official go-sdk:**

```go
import (
    "github.com/modelcontextprotocol/go-sdk/mcp"
    agentcat "go.agentcat.com/sdk/officialsdk/v2"
)

shutdown, err := agentcat.Track(mcpServer, "proj_0000000", &agentcat.Options{
    Identify: func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
        req := request.(*mcp.CallToolRequest) // always a tool call in v2
        _ = req // extract identity from the request, ctx, headers, or an auth token
        return &agentcat.UserIdentity{
            UserID: "user_12345", UserName: "demo_user",
            UserData: map[string]any{"email": "demo@example.com"},
        }
    },
})
```

### Redacting sensitive data

AgentCat redacts all data sent to its servers and encrypts at rest, but for additional security, it offers a hook to do your own redaction on all text data returned back to our servers.

```go
shutdown, err := agentcat.Track(mcpServer, "proj_0000000", &agentcat.Options{
    RedactSensitiveInformation: func(text string) string {
        return redact(text)
    },
})
```

For redaction decisions that need more context than a single string — such as which tool was called or what type of event is being published — use the event-level `RedactEvent` hook. It receives the full event object and returns a modified event, or `nil` to drop the event entirely. It can be combined with `RedactSensitiveInformation`.

```go
shutdown, err := agentcat.Track(mcpServer, "proj_0000000", &agentcat.Options{
    RedactEvent: func(event *agentcat.Event) (*agentcat.Event, error) {
        // Drop events from tools that handle secrets entirely
        if event.GetResourceName() == "get_credentials" {
            return nil, nil
        }
        // Strip response payloads from a specific tool
        if event.GetResourceName() == "export_report" {
            event.Response = nil
        }
        return event, nil
    },
})
```

### Vendor Support

AgentCat seamlessly integrates with your existing observability stack, providing automatic logging and tracing without the tedious setup typically required. Export telemetry data to multiple platforms simultaneously:

```go
shutdown, err := agentcat.Track(mcpServer, "proj_0000", &agentcat.Options{
    // Project ID can optionally be "" if you just want to forward telemetry
    Exporters: map[string]agentcat.ExporterConfig{
        "otlp": {
            Type:     "otlp",
            Endpoint: "http://localhost:4318", // /v1/traces is appended automatically
        },
        "datadog": {
            Type:    "datadog",
            APIKey:  os.Getenv("DD_API_KEY"),
            Site:    "datadoghq.com",
            Service: "my-mcp-server",
        },
        "sentry": {
            Type:        "sentry",
            DSN:         os.Getenv("SENTRY_DSN"),
            Environment: "production",
        },
        "posthog": {
            Type:   "posthog",
            APIKey: os.Getenv("POSTHOG_API_KEY"),
            Host:   "https://us.i.posthog.com", // Optional: defaults to US region
        },
    },
})
```

Learn more about our free and open source [telemetry integrations](https://docs.agentcat.com/telemetry/integrations).

### Internal diagnostics

To help us catch and fix broken installs, the SDK sends AgentCat a small, anonymized
signal when setup or runtime errors occur — never your tool calls, your responses,
or anything about your users. Records carry only operational metadata, such as your
project ID (or an anonymous install ID when none is set). Your local `~/agentcat.log`
is unchanged.

Diagnostics are on by default and can be turned off completely with either:

- `agentcat.Options{DisableDiagnostics: true}` passed to `Track`, or
- the `DISABLE_DIAGNOSTICS` environment variable.

## Free for open source

AgentCat is free for qualified open source projects. We believe in supporting the ecosystem that makes MCP possible. If you maintain an open source MCP server, you can access our full analytics platform at no cost.

**How to apply**: Email [hi@agentcat.com](mailto:hi@agentcat.com) with your repository link

_Already using AgentCat? We'll upgrade your account immediately._

## Community Cats 🐱

Meet the cats behind AgentCat! Add your cat to our community by submitting a PR with your cat's photo in the `docs/cats/` directory.

<div align="left">
  <img src="docs/cats/bibi.png" alt="bibi" width="80" height="80">
  <img src="docs/cats/zelda.jpg" alt="zelda" width="80" height="80">
</div>

_Want to add your cat? Create a PR adding your cat's photo to_ `docs/cats/` _and update this section!_
