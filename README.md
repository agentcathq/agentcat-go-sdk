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
  <a href="https://pkg.go.dev/go.agentcat.com/sdk"><img src="https://pkg.go.dev/badge/go.agentcat.com/sdk.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/go.agentcat.com/sdk"><img src="https://goreportcard.com/badge/go.agentcat.com/sdk" alt="Go Report Card"></a>
  <a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go" alt="Go Version"></a>
  <a href="https://github.com/agentcathq/agentcat-go-sdk/issues"><img src="https://img.shields.io/github/issues/agentcathq/agentcat-go-sdk.svg" alt="GitHub issues"></a>
  <a href="https://github.com/agentcathq/agentcat-go-sdk/actions"><img src="https://github.com/agentcathq/agentcat-go-sdk/workflows/CI/badge.svg" alt="CI"></a>
</p>

> [!IMPORTANT]
> **MCPcat is now AgentCat** 🐱 — same team, same product, new name. This module was previously published as `github.com/mcpcat/mcpcat-go-sdk`, which keeps working forever, but new features land here. Upgrading takes a few minutes — see the [migration guide](./MIGRATION.md).

> [!NOTE]
> Looking for the Python SDK? Check it out here [agentcat-python](https://github.com/agentcathq/agentcat-python-sdk).

## Why use AgentCat? 🤔

AgentCat helps developers and product owners build, improve, and monitor their MCP servers by capturing user analytics and tracing tool calls.

Use AgentCat for:

- **User session replay** 🎬. Follow alongside your users to understand why they're using your MCP servers, what functionality you're missing, and what clients they're coming from.
- **Trace debugging** 🔍. See where your users are getting stuck, track and find when LLMs get confused by your API, and debug sessions across all deployments of your MCP server.
- **Existing platform support** 📊. Get logging and tracing out of the box for your existing observability platforms (OpenTelemetry, Datadog, Sentry) — eliminating the tedious work of implementing telemetry yourself.

<img alt="AgentCat architecture — the AgentCat SDK inside your MCP server sends analytics to your observability vendors and session replay to the AgentCat dashboard" src="docs/static/architecture.png" />

## Supported MCP Libraries

AgentCat provides first-class support for the two most popular Go MCP libraries:

| Library | Install |
|---------|---------|
| [mcp-go](https://github.com/mark3labs/mcp-go) (mark3labs) | `go get go.agentcat.com/sdk/mcpgo` |
| [go-sdk](https://github.com/modelcontextprotocol/go-sdk) (official) | `go get go.agentcat.com/sdk/officialsdk` |

Import the package that matches the MCP library you're already using. Both expose the same `Track()` API and share the same feature set.

## Getting Started

Create an account and obtain your project ID at [agentcat.com](https://agentcat.com). For detailed setup instructions visit our [documentation](https://docs.agentcat.com).

Add one `Track()` call before starting your server:

**mark3labs/mcp-go:**
```go
import agentcat "go.agentcat.com/sdk/mcpgo"

shutdown, err := agentcat.Track(mcpServer, "proj_YOUR_PROJECT_ID", nil)
if err != nil { /* handle error */ }
defer shutdown(context.Background())
```

**Official go-sdk:**
```go
import agentcat "go.agentcat.com/sdk/officialsdk"

shutdown, err := agentcat.Track(mcpServer, "proj_YOUR_PROJECT_ID", nil)
if err != nil { /* handle error */ }
defer shutdown(context.Background())
```

`Track()` returns a shutdown function — call it before your application exits to flush all queued events.

## Advanced Features

### User Identification

Identify your user sessions with a callback to attach user information to every event in a session. The callback runs on every auto-captured event (tool calls, resource reads, initialize, and so on); every time it returns a non-nil identity, an `agentcat:identify` event is published and the session identity is updated (`UserID`/`UserName` are overwritten, `UserData` merges across calls). Return `nil` to skip a request — for example, type-assert `*mcp.CallToolRequest` to identify only on tool calls.

**mark3labs/mcp-go:**
```go
import (
    "github.com/mark3labs/mcp-go/mcp"
    agentcat "go.agentcat.com/sdk/mcpgo"
)

shutdown, err := agentcat.Track(s, "proj_YOUR_PROJECT_ID", &agentcat.Options{
    Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
        req, ok := request.(*mcp.CallToolRequest)
        if !ok {
            return nil // identify on tool calls only
        }
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
    agentcat "go.agentcat.com/sdk/officialsdk"
)

shutdown, err := agentcat.Track(s, "proj_YOUR_PROJECT_ID", &agentcat.Options{
    Identify: func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
        req, ok := request.(*mcp.CallToolRequest)
        if !ok {
            return nil // identify on tool calls only
        }
        _ = req // extract identity from the request, ctx, headers, or an auth token
        return &agentcat.UserIdentity{
            UserID: "user_12345", UserName: "demo_user",
            UserData: map[string]any{"email": "demo@example.com"},
        }
    },
})
```

### Sensitive Data Redaction

AgentCat redacts all data sent to its servers and encrypts at rest, but for additional security, it offers a hook to do your own redaction on all text data before it leaves your server.

```go
shutdown, err := agentcat.Track(s, "proj_YOUR_PROJECT_ID", &agentcat.Options{
    RedactSensitiveInformation: func(text string) string {
        return emailRegex.ReplaceAllString(text, "[REDACTED]")
    },
})
```

### Telemetry Exporters

Send every captured event to your existing observability stack — in addition to (or instead of) the AgentCat platform. Four exporters are available: `otlp`, `datadog`, `sentry`, and `posthog`. Exporters run fire-and-forget in parallel with the AgentCat API send; an exporter failure never affects your server or the other exporters.

```go
shutdown, err := agentcat.Track(s, "proj_YOUR_PROJECT_ID", &agentcat.Options{
    Exporters: map[string]agentcat.ExporterConfig{
        // OpenTelemetry (any OTLP/HTTP collector; /v1/traces is appended automatically)
        "otlp": {
            Type:     "otlp",
            Endpoint: "http://localhost:4318",
            Headers:  map[string]string{"Authorization": "Bearer TOKEN"}, // optional
        },
        // Datadog (logs + metrics)
        "datadog": {
            Type:    "datadog",
            APIKey:  os.Getenv("DD_API_KEY"),
            Site:    "datadoghq.com", // or datadoghq.eu, us3.datadoghq.com, ...
            Service: "my-mcp-server",
            Env:     "production", // optional
        },
        // Sentry (logs always; error events create Issues; transactions with EnableTracing)
        "sentry": {
            Type:          "sentry",
            DSN:           os.Getenv("SENTRY_DSN"),
            Environment:   "production", // optional
            Release:       "1.2.3",      // optional
            EnableTracing: true,         // optional, default false
        },
        // PostHog (batch capture; $exception on errors; $ai_span with EnableAITracing)
        "posthog": {
            Type:            "posthog",
            APIKey:          os.Getenv("POSTHOG_API_KEY"),
            Host:            "https://us.i.posthog.com", // optional, default shown
            EnableAITracing: true,                       // optional, default false
        },
    },
})
```

**Telemetry-only mode**: pass an empty project ID (`""`) with at least one exporter configured, and events go only to your exporters — no AgentCat account required.

```go
shutdown, err := agentcat.Track(s, "", &agentcat.Options{
    Exporters: map[string]agentcat.ExporterConfig{
        "otlp": {Type: "otlp", Endpoint: "http://localhost:4318"},
    },
})
```

### Debug Mode

Enable debug logging for troubleshooting. Debug logs are written to `~/agentcat.log`.

```go
shutdown, err := agentcat.Track(s, "proj_YOUR_PROJECT_ID", &agentcat.Options{Debug: true})
```

### Using with Existing Hooks (mcp-go only)

If your server already uses mcp-go hooks, pass them via `Options.Hooks` and AgentCat will append its hooks alongside yours:

```go
shutdown, err := agentcat.Track(s, "proj_YOUR_PROJECT_ID", &agentcat.Options{Hooks: hooks})
```

### Internal diagnostics

To help us catch and fix broken installs, the SDK sends AgentCat a small, anonymized
signal when setup or runtime errors occur — never your tool calls, your responses,
or anything about your users. Records carry only operational metadata, such as your
project ID (or an anonymous install ID when none is set), SDK version, and Go
runtime/OS/arch. Your local `~/agentcat.log` is unchanged.

Diagnostics are on by default and can be turned off completely with either:

- `agentcat.Options{DisableDiagnostics: true}` passed to `Track`, or
- the `DISABLE_DIAGNOSTICS` environment variable.

## Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `DisableReportMissing` | `bool` | `false` | When `true`, prevents the `get_more_tools` tool from being registered |
| `DisableToolCallContext` | `bool` | `false` | When `true`, prevents the `context` parameter from being injected on tool calls |
| `Debug` | `bool` | `false` | Enable debug logging to `~/agentcat.log` |
| `RedactSensitiveInformation` | `func(string) string` | `nil` | Custom redaction applied to all text data before sending |
| `Identify` | callback | `nil` | Runs on every auto-captured event to attach user information to sessions; publishes an `agentcat:identify` event whenever it returns a non-nil identity |
| `Hooks` | `*server.Hooks` | `nil` | Pre-existing hooks to merge with (mcp-go only) |
| `Exporters` | `map[string]ExporterConfig` | `nil` | Telemetry exporters (`otlp`, `datadog`, `sentry`, `posthog`); with at least one exporter, the project ID may be empty (telemetry-only mode) |
| `APIBaseURL` | `string` | `https://api.agentcat.com` | Override the AgentCat API endpoint; falls back to `AGENTCAT_API_URL`, then the legacy `MCPCAT_API_URL` env var |

## Free for open source

AgentCat is free for qualified open source projects. We believe in supporting the ecosystem that makes MCP possible. If you maintain an open source MCP server, you can access our full analytics platform at no cost.

**How to apply**: Email hi@agentcat.com with your repository link

_Already using AgentCat? We'll upgrade your account immediately._

## Community Cats 🐱

Meet the cats behind AgentCat! Add your cat to our community by submitting a PR with your cat's photo in the `docs/cats/` directory.

<div align="left">
  <img src="docs/cats/bibi.png" alt="bibi" width="80" height="80">
  <img src="docs/cats/zelda.jpg" alt="zelda" width="80" height="80">
</div>

_Want to add your cat? Create a PR adding your cat's photo to `docs/cats/` and update this section!_
