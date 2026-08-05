# Migrating from `github.com/mcpcat/mcpcat-go-sdk` to `go.agentcat.com/sdk`

MCPcat is now **AgentCat** — same team, same product, new name. The Go module has been renamed from `github.com/mcpcat/mcpcat-go-sdk` to [`go.agentcat.com/sdk`](https://pkg.go.dev/go.agentcat.com/sdk).

> **Going to v2?** Everything before the final section describes the v1.x rename only, and uses the unversioned `go.agentcat.com/sdk…` import paths. AgentCat v2 moves to `/v2` paths and removes MCP transport sessions, so read **"v2.0.0 — Explicit session handles"** at the end of this file — it supersedes anything above it that conflicts.

## Nothing breaks if you stay

We keep every existing surface alive **permanently** — not on a deprecation timer:

- The `github.com/mcpcat/mcpcat-go-sdk` module stays published and is **not retracted** — existing `go.mod` pins keep resolving and building forever
- `api.mcpcat.io` keeps accepting events forever (the new SDK defaults to `api.agentcat.com`, but the old endpoint is still accepted via the `APIBaseURL` option or the `MCPCAT_API_URL` environment variable)
- The `MCPCAT_API_URL` environment variable keeps working as a legacy fallback (the new SDK reads `AGENTCAT_API_URL` first, then `MCPCAT_API_URL`)
- Your project, data, and history stay unified regardless of which SDK sends them

If you never touch your integration, nothing stops working. Migrate on your own schedule — new features only land in `go.agentcat.com/sdk`.

## What changed

|                          | old                                             | new                                  |
| ------------------------ | ----------------------------------------------- | ------------------------------------ |
| Root module              | `github.com/mcpcat/mcpcat-go-sdk`               | `go.agentcat.com/sdk`                |
| mcp-go adapter           | `github.com/mcpcat/mcpcat-go-sdk/mcpgo`         | `go.agentcat.com/sdk/mcpgo`          |
| Official go-sdk adapter  | `github.com/mcpcat/mcpcat-go-sdk/officialsdk`   | `go.agentcat.com/sdk/officialsdk`    |
| Root package name        | `package mcpcat`                                | `package agentcat`                   |
| Generated API client     | `github.com/mcpcat/mcpcat-go-api`               | `go.agentcat.com/api`                |
| GitHub repo              | `github.com/MCPCat/mcpcat-go-sdk`               | `github.com/agentcathq/agentcat-go-sdk` |

The adapter packages are still named `mcpgo` and `officialsdk`; only their import paths changed. `Track()`, `Options`, `UserIdentity`, the redaction hook, and the shutdown function are unchanged.

The `Identify` callback now runs on every auto-captured event (not just tool calls) and receives the MCP request that triggered the event, so its signature is wider:

```diff
  // mcpgo
- Identify: func(ctx context.Context, req *mcp.CallToolRequest) *agentcat.UserIdentity {
+ Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
+     req, ok := request.(*mcp.CallToolRequest)
+     if !ok {
+         return nil // identify on tool calls only
+     }
      ...
  }

  // officialsdk
- Identify: func(ctx context.Context, req *mcp.CallToolRequest) *agentcat.UserIdentity {
+ Identify: func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
+     req, ok := request.(*mcp.CallToolRequest)
+     if !ok {
+         return nil // identify on tool calls only
+     }
      ...
  }
```

An `agentcat:identify` event is published every time the callback returns a non-nil identity (`UserID`/`UserName` are overwritten, `UserData` merges across calls); return `nil` to skip a request.

**New defaults in `go.agentcat.com/sdk` (the old names still work where noted):**

- The default API endpoint is now `https://api.agentcat.com`. `api.mcpcat.io` keeps accepting events and can still be used via the `APIBaseURL` option or an env var override.
- The SDK reads `AGENTCAT_API_URL` first, then falls back to the legacy `MCPCAT_API_URL` — existing `MCPCAT_API_URL` configurations keep working unchanged.
- Debug logs are written to `~/agentcat.log` with an `[AgentCat]` prefix. The old `~/mcpcat.log` is **no longer written** — update anything that tails it.
- The identify event type on the wire is now `agentcat:identify` (previously `mcpcat:identify`).
- The instance type is now `AgentCatInstance`; `MCPcatInstance` remains as a deprecated alias, so existing code keeps compiling.

**Unchanged on purpose (for compatibility):**

- The `MCPCAT_API_URL` environment variable — still honored as a fallback
- The `DISABLE_DIAGNOSTICS`, `DIAGNOSTICS_ENDPOINT`, and `DIAGNOSTICS_TOKEN` environment variables
- Your project ID — do not change it

## Steps

1. **Add the new module** (pick the adapter for your MCP library):

   ```bash
   go get go.agentcat.com/sdk/mcpgo        # mark3labs/mcp-go
   # or
   go get go.agentcat.com/sdk/officialsdk  # official modelcontextprotocol/go-sdk
   ```

2. **Update your imports:**

   ```diff
   - import mcpcat "github.com/mcpcat/mcpcat-go-sdk/mcpgo"
   + import agentcat "go.agentcat.com/sdk/mcpgo"

   - shutdown, err := mcpcat.Track(server, "proj_0000000", nil)
   + shutdown, err := agentcat.Track(server, "proj_0000000", nil)
   ```

   If you import the root module directly, `github.com/mcpcat/mcpcat-go-sdk` becomes `go.agentcat.com/sdk` and the package identifier `mcpcat` becomes `agentcat`.

3. **Tidy:**

   ```bash
   go mod tidy
   ```

   The old `github.com/mcpcat/mcpcat-go-sdk` requirement drops out of your `go.mod` automatically once nothing imports it.

Your project ID does not change, and your dashboard history is continuous.

## Or let an AI agent do it

Paste this into your coding agent from your project root:

```text
Migrate this project from the Go module `github.com/mcpcat/mcpcat-go-sdk` to its renamed successor `go.agentcat.com/sdk` (same API, new module path and package name):

1. Run `go get go.agentcat.com/sdk/mcpgo` if this project uses mark3labs/mcp-go, or `go get go.agentcat.com/sdk/officialsdk` if it uses the official modelcontextprotocol/go-sdk. If it imports the root module, also `go get go.agentcat.com/sdk`.
2. Update every import of "github.com/mcpcat/mcpcat-go-sdk/..." to the matching "go.agentcat.com/sdk/..." path (mcpgo -> mcpgo, officialsdk -> officialsdk, root -> root).
3. The root package identifier changes from `mcpcat` to `agentcat`; adapter packages commonly use an import alias, so keep or rename aliases so all qualified references still compile.
4. Do NOT change the project ID passed to Track() — it stays the same.
5. The MCPCAT_API_URL environment variable and api.mcpcat.io endpoint overrides keep working, so leave them if present (AGENTCAT_API_URL and api.agentcat.com are the new preferred names). Note the debug log moved from ~/mcpcat.log to ~/agentcat.log — update anything that reads the old path.
6. Run `go mod tidy`, then the project's build and tests to verify, and report anything referencing mcpcat that could not be migrated mechanically.
```

## FAQ

**Do I have to migrate?** No — and there is no deadline. The old module and endpoint stay up permanently.

**Will my data/history split?** No. Both SDKs report into the same platform and your history stays unified under your project.

**Does `Track()` or any option behave differently?** In v1.x, no: `Track()`, `Options`, `UserIdentity`, and the redaction hook work as before. The `Identify` callback runs on every auto-captured event and takes the triggering request (`request any` in mcpgo, `request mcp.Request` in officialsdk) — type-assert `*mcp.CallToolRequest` and return `nil` for other requests to keep the old tool-call-only behavior (see "What changed" above). Defaults were rebranded: events go to `api.agentcat.com` (the old endpoint still works via override), debug logs go to `~/agentcat.log`, and identify events are published as `agentcat:identify`. The new SDK also adds telemetry exporters (`otlp`, `datadog`, `sentry`, `posthog`) and a telemetry-only mode — see the README. **In v2 this answer no longer holds** — `Identify` runs per tool call, there is no identify event at all, and several APIs are gone; see the v2 section below.

**Questions?** Open an issue or email [hi@agentcat.com](mailto:hi@agentcat.com).

## v2.0.0 — Explicit session handles (MCP 2026-07-28)

**Shipping as `v2.0.0`.** Import paths changed to
`go.agentcat.com/sdk/v2`, `go.agentcat.com/sdk/mcpgo/v2`,
`go.agentcat.com/sdk/officialsdk/v2`, so v1 keeps resolving untouched and you
can adopt v2 one service at a time.

```bash
go get go.agentcat.com/sdk/mcpgo/v2        # mark3labs/mcp-go
go get go.agentcat.com/sdk/officialsdk/v2  # official go-sdk
```

**You do not have to upgrade your MCP library to adopt v2.** Supported ranges
are `modelcontextprotocol/go-sdk v1.4.1 – v1.7.0` and
`mark3labs/mcp-go v0.53.0 – v0.57.0`; a fresh `go get` pulls the newest, but an
existing pin anywhere in the range keeps working and is tested with `-race` in
CI. The one exception is mcp-go **below v0.53.0**: v2 installs its tools/call
middleware through `MCPServer.Use`, which mcp-go did not have until v0.47.0,
and its test suite needs server options that landed in v0.53.0. If you are
pinned below that, bump mcp-go when you move to v2.

Two features depend on what your pinned version can express, and AgentCat
reports their absence rather than guessing:

- **Integers above 2^53** keep their exact value only on `mcp-go v0.56.0+`,
  which is when the library began preserving the original JSON bytes
  alongside the decoded value. Below that a large integer rounds through
  `float64` before AgentCat sees it — the library's own behavior, unchanged by
  AgentCat's argument stripping or handle mirror.
- **The `agentcat_mrtr` tag** requires `go-sdk v1.7.0+`. Multi-round-trip did
  not exist before MCP 2026-07-28, so on older versions there are no
  intermediate rounds to tag. (mcp-go has no multi-round-trip support at all,
  so the tag never appears on that adapter.)

Everything else — session correlation, argument stripping, event capture,
redaction, exporters, `get_more_tools` — behaves identically across the whole
range.

MCP 2026-07-28 removed protocol sessions, so AgentCat v2 replaces all
transport-session correlation with explicit stateless handles:

- Each tool schema gains an **optional `session_id` parameter**. When a call
  omits it, the SDK mints a `ses_` ID and tells the agent to echo it via a
  trailing `[MCP INSTRUCTIONS]` text block and a `_mcp_instructions` field in
  `structuredContent`. A supplied handle is trusted verbatim **if this server
  issued it** — see "Session IDs are validated" below. Dashboards are
  unaffected: the session ID rides in the existing `sessionId` event field.
  Injection is per-tool and skippable: a tool that already declares
  `session_id` (or `context`) keeps its own, and only that one parameter is
  skipped; a composed `oneOf`/`allOf`/`anyOf` input schema gets no injected
  parameters at all.
- **Removed events**: `mcp:initialize`, `mcp:tools/list`, `agentcat:identify`,
  and all non-tool `mcp:*` events. v2 publishes only `mcp:tools/call` and
  `agentcat:custom`.
- **`Identify` now runs on every tool call** and its result is stamped onto
  that call's event. There is no identity cache and no identify event — keep
  the hook cheap.
- **Callbacks receive the live request context.** `Identify`, `EventTags`, and
  `EventProperties` run inline on the tool call's own goroutine in **both**
  adapters, and the `ctx` they receive is the request's own — already done if
  the client cancelled the call or disconnected. A callback that does
  context-aware I/O (a DB or HTTP lookup on that `ctx`) will fail on a
  cancelled call, and the event then publishes without identity or tags rather
  than with them. Read values off `ctx` instead of making calls with it, or
  pass your own `context.Background()`-derived context to any lookup you must
  do. This is new for `officialsdk`, which previously captured on a detached
  goroutine with a `context.WithoutCancel` copy; `mcpgo` has always passed the
  live context.
- **`ResolveSessionID` (new, per adapter)**: bring your own correlation ID; the
  SDK derives the same `ses_` session from it deterministically across
  processes and languages. In hook mode no `session_id` parameter is injected
  and no instructions are shown.
- **The `context` parameter is injected as `required`.** Unlike `session_id`, the
  `context` parameter AgentCat adds to each advertised tool is marked
  `required` in the advertised schema (this is unchanged from v1, but it
  matters more now that closed input schemas are common — see below). Omitting
  it never rejects a call: AgentCat strips the parameter before your handler
  runs, and it is your server, not AgentCat, that decides what a missing
  argument means.
- **`EnableAgentTracking` (new, default off)**: injects an agent-self-chosen
  `agent_id` parameter, marked `required` where it is injected; carried as
  event tags. Like `session_id` and `context`, injection is best-effort per tool:
  a tool that already declares its own `agent_id` keeps it, and a composed or
  unreadable input schema is left alone.
- **New SDK-owned event tags.** Every published event now carries tags
  AgentCat sets itself. They appear in your dashboards, in `RedactEvent`, and
  in every configured exporter, and they are exempt from the 50-tag customer
  cap. If you assert on an event's exact tag set, or forward tags to a system
  with a tag budget, account for these:

  | tag | value |
  | --- | ----- |
  | `agentcat_session_id_source` | `supplied`, `minted`, `hook`, `invalid`, or `foreign` |
  | `agentcat_agent_id` | the agent's self-chosen ID (only with `EnableAgentTracking`) |
  | `agentcat_agent_id_source` | `supplied` (only when an `agent_id` was sent) |
  | `agentcat_protocol_version` | the negotiated MCP protocol version, when the client reports one |
  | `agentcat_mrtr` | `input_required` or `continuation`, on multi-round tool calls only |

  A tag of your own with one of these names is overwritten: the SDK's value wins.
- **mcp-go: AgentCat now declares its parameters on your REGISTERED tool
  schemas**, not just the advertised copies. mcp-go validates a call's
  arguments against the registered input schema *before* any middleware can
  strip them, and a result against the registered output schema *after*, so a
  server using `WithInputSchemaValidation`, `WithStrictInputSchemaDefault`,
  `WithOutputSchemaValidation`, or `mcp.WithSchemaAdditionalProperties(false)`
  would otherwise reject the calls and results AgentCat's advertised schemas
  ask for. The declarations are **optional properties only**: your `required`
  list and your `additionalProperties` setting are never changed. A schema that
  gains a declaration carries it in raw form afterwards, so
  `MCPServer.ListTools()` returns `RawInputSchema`/`RawOutputSchema` for that
  tool rather than the structured fields. The declaration is written directly
  onto the registered tool rather than re-registered, so it never emits
  `notifications/tools/list_changed`.
- **New adapter-level `Event` re-export.** Both adapters now export
  `Event` (`mcpgo.Event`, `officialsdk.Event`) as an alias for the published
  event type, so a `RedactEvent` hook no longer needs to import the root
  module:

  ```diff
  - RedactEvent: func(event *agentcat.Event) (*agentcat.Event, error) {
  + RedactEvent: func(event *mcpgo.Event) (*mcpgo.Event, error) {
  ```

  The old form still compiles — they are the same type.
- **Custom events**: `PublishCustomEvent(sessionID string, …)` now uses the
  string **verbatim** as the session ID (no derivation). Prefer the new
  `CustomEventData.SessionID` field. Tracked-server custom events publish
  without a session unless one is given.
- **Removed APIs**: `Session`, `SessionMap`, `ProtectedSession`,
  `NewSessionMap`, `NewSessionID`, `DeriveSessionID`,
  `IsPlaceholderSessionID`, `MergeIdentities`, `CreateIdentifyEvent`,
  `NewEvent`, `AgentCatInstance.SessionID`, `AgentCatInstance.ServerRef`,
  and mcpgo's `Options.Hooks` (AgentCat now composes with your hooks via
  `MCPServer.GetHooks()`).
- **Per-request factories** (`NewStreamableHTTPHandler(getServer, …)` with
  `Stateless: true`): call `Track()` inside the factory. Module-level state
  initializes once; per-server state is released when the server object is
  garbage collected. `AgentCatInstance.ServerRef` was removed to make that
  possible: the registry holds the instance for the process lifetime, so a
  strong reference back to the server pinned every server a factory ever
  built. Read the server from your own scope instead.

### Session IDs are validated

AgentCat trusts only a `session_id` **it issued**: the `ses_` prefix followed
by a 27-character base62 KSUID, which is exactly what `MintSessionID` and
`DeriveSessionID` produce. `Event.SessionId` is exempt from both redaction
hooks — the redactor snapshots and restores it around `RedactEvent` and
`RedactSensitiveInformation` alike — so a value adopted there reaches every
configured exporter with no opportunity to scrub it. Anything AgentCat did not
mint is therefore never adopted.

Three things change that you can observe:

- **A hand-written `session_id` no longer correlates.** If you or an agent
  send a value that is not in that shape, the call publishes **without a
  session**, tagged `agentcat_session_id_source: invalid`, and the agent is
  told to re-send the ID this server issued it. No replacement handle is
  issued: if a value arrived at all, the agent already has a good one. The
  value you sent is still recorded in the event's request arguments, where
  `RedactEvent` and `RedactSensitiveInformation` can reach it.

- **A tool that declares its own `session_id` publishes sessionless.** Your
  parameter is untouched and still reaches your handler, AgentCat reads
  nothing from it, and the agent is told nothing about it. Those calls are
  tagged `foreign` and cannot be correlated with anything. An error naming the
  tool goes to `~/agentcat.log` once per tool. If you already manage sessions,
  set `Options.ResolveSessionID` and AgentCat will derive its session from
  your identifier and stop injecting `session_id` entirely.

- **Tools with composed, opaque, or malformed input schemas now publish
  sessionless too.** AgentCat never injected `session_id` into those (a
  `oneOf`/`allOf`/`anyOf` schema gets no injected parameters at all), so it
  does not read one back out of them either. Previously such calls minted a
  handle and minted it back; now they are tagged `foreign` and say nothing.
  This matches the TypeScript SDK. If you want those tools correlated, give
  them a plain-object input schema or use `ResolveSessionID`.

Hook mode is unaffected: it is decided before any argument is read, so a
`ResolveSessionID` server behaves exactly as before on every tool, including
ones that declare their own `session_id`.

Sessionless events send `"session_id": null` on the wire, and the PostHog,
OTLP, Datadog and Sentry exporters omit their session attributes entirely
rather than deriving one from an empty string.
