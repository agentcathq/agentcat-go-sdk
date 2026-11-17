# Changelog

This file lists the notable changes of each release of the AgentCat Go SDK. The three modules
`go.agentcat.com/sdk/v2`, `go.agentcat.com/sdk/mcpgo/v2` and `go.agentcat.com/sdk/officialsdk/v2`
release together with one version.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the SDK follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Each `mcp:tools/call` event now carries `input_tokens` and `output_tokens`: estimates of the
  tokens of the raw tool arguments and of the text that the model reads back from the result,
  for both adapters. The SDK computes them before your redaction hooks run, so they describe the
  original size. To keep the sizes on your server, drop the two fields in `RedactEvent`. A failure
  to estimate never affects the tool result. The root module adds `ApplyTokenEstimates` for the
  adapters (#15).
- The SDK now sends AgentCat a description of your MCP server, for both adapters: its name,
  version, title and instructions, and the lists of its tools, resources, resource templates and
  prompts. About once an hour per process and server name, after your server answers a request,
  the SDK calls your server's own list handlers and sends the result in the background. In a
  runtime that keeps no memory between requests, it sends only on a `tools/list` request or a
  `get_more_tools` call. `RedactSensitiveInformation` runs on every text; item names and URIs
  stay as they are. No option changes: the send is on when tracking is on, and a failed send
  never reaches your server. The root module adds `MaybeStartServerInfo` and
  `ServerInfoTriggerForTool` for the adapters (#18).

### Changed

- A tool result with no content blocks now records `"content": []` in the event instead of
  leaving out the key (#15).
- The SDK requires `go.agentcat.com/api` v1.0.5, which adds the two token fields to the publish
  request (#15) and the server info request (#18).

## [2.1.0] - 2026-08-29

There are no Go API changes for adapter users. The changes are in the wire contract between your
server and the agent. The [migration guide](https://github.com/agentcathq/agentcat-go-sdk/blob/main/MIGRATION.md#v210--sturdier-session-handles)
has the details.

### Changed

- The text block that announces a new `session_id` is now the first content element of the
  result, not the last, so it survives clients that truncate long results.
- `session_id` is now in the `required` list of each tool, with the pattern
  `^(start|ses_[0-9A-Za-z]{27})$`. A call that leaves it out is still accepted as `start`. On
  mcp-go, the registered schema keeps the injected parameters optional, so
  `WithInputSchemaValidation` never rejects such a call.
- The `structuredContent` mirror is now `mcp_session` (`{session_id?, agent_id?, status?}`) and
  replaces `_mcp_instructions`. The `status` member (`issued`, `active` or `unrecognized`)
  replaces the prose `instructions` member.
- An unrecognized `session_id` is reported as `mcp_session.status: "unrecognized"` and in a
  leading `[session_id unrecognized …]` text block.
- The `session_id` and `agent_id` descriptions are shorter, and the two `agent_id` descriptions
  are now one.
- Root module only: `agentcat.MCPInstructionsKey` is renamed `agentcat.MCPSessionKey`.

## [2.0.0] - 2026-08-05

AgentCat v2 replaces transport-session correlation with explicit `session_id` handles, for the
stateless transports of MCP 2026-07-28. For most integrations the upgrade is the `/v2` import
path and `go mod tidy`. The [migration guide](https://github.com/agentcathq/agentcat-go-sdk/blob/main/MIGRATION.md)
has the details.

### Added

- Task correlation through explicit handles: `session_id` is injected into the input schema of
  each tracked tool, and the agent sends it back on each call. `ResolveSessionID` derives the
  session from your own identifier instead.
- AgentCat accepts only a `session_id` that it issued. Every event carries the
  `agentcat_session_id_source` tag (`minted`, `supplied`, `hook`, `invalid` or `foreign`).
- Opt-in agent attribution with `agent_id` (`EnableAgentTracking`).
- The same `Options` on both adapters, `go.agentcat.com/sdk/mcpgo/v2` and
  `go.agentcat.com/sdk/officialsdk/v2`, with support for per-request server factories and for
  the strict schema validation of mcp-go.
- Telemetry exporters (`otlp`, `datadog`, `sentry`, `posthog`), `EventTags`, `EventProperties`,
  `PublishCustomEvent` and the `RedactEvent` hook.

### Changed

- Module paths move to `/v2`. The v1 paths keep resolving.
- v2 publishes only `mcp:tools/call` and `agentcat:custom` events.
- `Identify` runs on each tool call and stamps only the event of that call.
- Agent tracking is off by default.
- `PublishCustomEvent` takes session IDs as they are; `CustomEventData.SessionID` replaces the
  session references.

### Removed

- Transport-session handling, identity caches and identify events.

### Fixed

- A failure in AgentCat never stops your server: each customer callback is panic-guarded,
  per-request servers no longer leak, integers above 2^53 reach your handler unchanged, and
  logging goes silent instead of writing to stdout when the log file cannot be created.

## [1.0.0] - 2026-07-04

### Changed

- MCPcat is now AgentCat. This is the first stable release of the AgentCat Go SDK, with the
  module paths `go.agentcat.com/sdk`, `go.agentcat.com/sdk/mcpgo` and
  `go.agentcat.com/sdk/officialsdk`. The old module `github.com/mcpcat/mcpcat-go-sdk` stays
  published and is not retracted. `MIGRATION.md` has the steps.

## Older releases

The [GitHub releases](https://github.com/agentcathq/agentcat-go-sdk/releases) have the notes of
the versions before 1.0.0.

[Unreleased]: https://github.com/agentcathq/agentcat-go-sdk/compare/v2.1.0...HEAD
[2.1.0]: https://github.com/agentcathq/agentcat-go-sdk/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/agentcathq/agentcat-go-sdk/compare/v1.0.0...v2.0.0
[1.0.0]: https://github.com/agentcathq/agentcat-go-sdk/releases/tag/v1.0.0
