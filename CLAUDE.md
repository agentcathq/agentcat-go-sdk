# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

AgentCat Go SDK — an analytics agent that MCP server maintainers integrate to capture usage telemetry from tool calls (arguments, responses, user intent, actor identity). It supports two MCP libraries via separate Go modules: `mcpgo/` (mark3labs/mcp-go) and `officialsdk/` (modelcontextprotocol/go-sdk).

**This is v2.** MCP 2026-07-28 removed protocol-level sessions, so v2 replaces all transport-session correlation with explicit, stateless, per-request session handles. Module paths are `go.agentcat.com/sdk/v2`, `go.agentcat.com/sdk/mcpgo/v2`, `go.agentcat.com/sdk/officialsdk/v2`. `MIGRATION.md` is the source of truth for the v1→v2 delta and the full list of removed APIs.

## Build & Test Commands

```bash
make all              # fmt + vet + test (default)
make test             # all tests with -race
make test-mcpgo       # mcpgo tests only
make test-officialsdk # officialsdk tests only
make coverage         # tests + coverage HTML report
make fmt              # go fmt ./...
make fmt-check        # CI-style format check (fails on unformatted code)
make vet              # go vet ./...
make check            # fmt-check + vet + test
```

Run a single test: `go test -v -run TestName ./path/to/package/...`

This is a Go workspace (`go.work`) — run commands from the repo root.

## Module Layout

```
go.work                     # workspace: root + mcpgo + officialsdk + examples (incl. factory)
mcpcat.go / types.go        # root module — shared integration API & type aliases
custom_event.go             # PublishCustomEvent (agentcat:custom)
context_description.go      # default "context" parameter copy
diagnostics.go              # InitDiagnostics / LogSetupComplete facade for adapters
metadata.go                 # AttachEventMetadata: shared tags/properties plumbing
mcpgo/                      # mcp-go adapter (separate go.mod, depends on mark3labs/mcp-go)
  mcpgo.go                  # Track() entry point, Options, hook composition via GetHooks()
  list_inject.go            # tools/list hook: schema injection + registry publication
  call_middleware.go        # tools/call middleware: resolve, strip, capture, mint back
  registered_schema.go      # declares mcp_session on registered output schemas
  failure_hooks.go          # error-path hooks so failed calls still publish
  get_more_tools.go         # registers the optional get_more_tools tool
  extract.go / metadata.go  # request metadata, EventTags/EventProperties plumbing
officialsdk/                # official go-sdk adapter (separate go.mod)
  officialsdk.go            # Track() entry point, Options
  middleware.go             # receiving middleware: resolve, strip, capture, mint back, MRTR
  list_inject.go            # tools/list injection + registry publication
  get_more_tools.go         # registers the optional get_more_tools tool
  extract.go / metadata.go  # request metadata, EventTags/EventProperties plumbing
internal/                   # shared internals (only importable by root module)
  constants/                # byte-pinned agent-facing copy, param names, wire keys, tags
  handles/                  # mint / derive / validate / resolve session and agent handles
  inject/                   # pure schema-injection engine, registries, strip, mirror
  core/                     # types: Options, Event, AgentCatInstance, UserIdentity
  publisher/                # async event publisher with worker pool → AgentCat API
  event/                    # event construction, EventContext, SDK tags
  exporters/                # otlp / datadog / sentry / posthog fan-out
  redaction/                # applies RedactFunc / RedactEvent before publish
  registry/                 # server→AgentCatInstance map, released via runtime.AddCleanup
  logging/                  # file-backed debug logger (~/agentcat.log)
  diagnostics/              # anonymized SDK setup/runtime error reporting
  validation/ sanitization/ truncation/ walk/ exceptions/   # payload hygiene helpers
examples/                   # runnable examples: basic + advanced per adapter, plus
                            # officialsdk/factory (stateless per-request Track())
```

## Architecture

**Three-module design**: The root module (`go.agentcat.com/sdk/v2`) holds all shared logic in `internal/`. The two adapter modules (`mcpgo/` and `officialsdk/`) each have their own `go.mod` and depend on the root module plus their respective MCP library. Users import only the adapter matching their MCP library. **Adapters cannot import `internal/`** across the module boundary — they consume re-exports from `mcpcat.go`/`types.go`.

**Track() is the single entry point** in both adapters. It registers the server, initializes the global publisher singleton, and installs the tools/list injection hook plus the tools/call middleware (mcp-go) or the receiving middleware (official go-sdk).

**Explicit handles, no sessions.** Each advertised tool gains a `session_id` parameter — `required` in the advertised schema, with pattern `^(start|ses_[0-9A-Za-z]{27})$`; mcpgo's registered-schema declaration keeps it optional so strict input validation never rejects a call that omits it — plus `agent_id` (required, only when `EnableAgentTracking`) and `context` — subject to the per-tool skip rules noted below. On a call with no `session_id`, or with the `start` sentinel, the SDK mints one and prepends a `[session_id issued …]` text block as the FIRST content element of the wire result — only on the minting call; an unrecognized value gets a leading `[session_id unrecognized …]` block and no replacement. Tools whose declared output schema was extended also get an `mcp_session` mirror in `structuredContent` on every response: `{session_id?, agent_id?, status?}` with `status` one of `issued`/`active`/`unrecognized`; hook-mode and foreign resolutions carry `agent_id` alone, and an empty payload mirrors nothing. All of this is **wire-only**: published events always carry the customer's raw unstripped request and their original undecorated response. Injected parameters (`session_id`, `agent_id`, `context`) are stripped from the arguments before the customer's handler runs. Configuring `Options.ResolveSessionID` switches to **hook mode**: no `session_id` is injected anywhere and no session instructions are ever shown to the agent.

**AgentCat trusts only handles it issued.** `handles.IsValidSessionID` (`^ses_[0-9A-Za-z]{27}$`) gates every supplied value, because `Event.SessionId` is exempt from both redaction hooks. `SessionSource` has five members: `supplied`, `minted`, `hook`, plus `invalid` (the parameter is ours, the value is not) and `foreign` (the parameter is the customer's own). The last two publish **sessionless** — an explicit JSON `null`, never `""`. Ownership comes from `inject.SessionParamIsOurs(tool, reg)`, so **both adapters must load the registries BEFORE resolving handles**. Two ordering rules are load-bearing and each has a test: hook mode is checked before ownership (a hook-mode server injects nothing, so ownership would be false everywhere), and a tool missing from the registries counts as **ours** (a call can precede any `tools/list`). `handles.BuildMintBackText(res)` is the single decision point for whether a call announces anything — adapters must never re-derive it. A customer-declared `session_id` is recorded in `Registries.CustomerOwnedParams` and reported once per tool by `ReportSessionParamCollisions`, so `inject.Build` stays pure.

**Pure engine in the root module.** `internal/inject.Build(cfg, tools)` is deterministic — identical config plus identical tools must always produce identical schemas and registries, because per-request factories rebuild registries on demand on the first `tools/call` when no `tools/list` preceded it. `internal/handles` owns mint/derive/validate/resolve; `internal/constants` pins agent-facing copy byte-for-byte against the other AgentCat SDKs.

**Only `mcp:tools/call` and `agentcat:custom` are published.** `mcp:initialize`, `mcp:tools/list`, `agentcat:identify`, and all resource/prompt events were removed in v2. `Identify` runs per tool call, uncached, and stamps only that event.

**Publisher** is a global singleton (`sync.Once`) with a buffered channel queue and worker pool. Events are enqueued via `Publish()`, redacted if configured, and sent to the AgentCat API. `Shutdown()` drains the queue with a context deadline (default 5s).

**Registry** maps server instances → `AgentCatInstance` (project ID + options + registries) via a `sync.RWMutex`-guarded map keyed by pointer address, with entries released through `runtime.AddCleanup`. Nothing reachable from an instance may hold a **strong** reference back to its server — the per-request factory topology (`Track()` inside `getServer`) would otherwise leak one entry per request. This is why v1's `AgentCatInstance.ServerRef` is gone and mcpgo's `RebuildTools` closes over the server via `weak.Make` (officialsdk's closes weakly over a `rebuildTarget` holder of the next handler, whose only strong owner is the server's own middleware handler). The same discipline extends to everything AgentCat parks on customer-owned objects: mcpgo instruments each `*server.Hooks` value exactly once with closures that capture no server — they resolve the live server from the request context and its capturer through the weak `hook_state` side maps — because a customer's long-lived shared Hooks value would otherwise pin every dead per-request server through the appended closures.

**Two optional extras beyond the handles**: the `get_more_tools` tool, which AgentCat registers on the server so an agent can report capabilities you do not offer (`DisableReportMissing`), and the `context` parameter injected to capture user intent (`DisableToolCallContext`). Injection is never unconditional — `inject.Build` skips a tool that already declares the property itself, and skips parameter injection entirely for composed (`oneOf`/`allOf`/`anyOf`) or malformed input schemas. When writing docs, say "each tool", not "every tool".

## Key Conventions

- Type aliases in `types.go` re-export `internal/core`, `internal/constants`, `internal/handles`, and `internal/inject` so adapters import from root, not internal
- Both adapters re-export every type the public API mentions — `UserIdentity`, `Event`, `AgentCatInstance`, `CustomEventData`, `ExporterConfig` — so end users never import the root module. Adding a type to a public signature means adding its alias to both adapters' alias blocks in the same change
- Agent-facing copy lives only in `internal/constants` and is byte-identical to the TypeScript SDK — do not reword it here alone
- **`Task` in a Go identifier is mcp-go's protocol task augmentation** (`AddTaskTool`, `TaskToolHandlerFunc`, `CreateTaskResult`, `request.Params.Task`) — a detached long-running execution, never an AgentCat session. AgentCat has no "task" identifier: a grep for `Task` in Go code is a precise filter for mcp-go's concept. The English noun survives only in agent-facing copy, where it names the agent's unit of work ("for a given task", "your entire task")
- Tests live alongside implementation (`_test.go` in same package); use table-driven tests
- Concurrency-sensitive code (registry, publisher, logging) needs `go test -race`
- CI (`.github/workflows/ci.yml`) has four jobs: `gofmt` check, `go test -race` per module (Go 1.24 + stable), `make vet`, and `make build-examples`. The local equivalent is `make check && make test-mcpgo && make test-officialsdk && make build-examples` — `make check` alone is **weaker than CI**, because its `test` step is `go test ./...` from the root and so runs zero adapter tests (`hooks/pre-commit` inherits this)
- `go vet ./...` and `go test ./...` from the root cover **only** the root module — nested modules are excluded from `./...` with or without `go.work`. That is why `vet` iterates `$(MODULES)` and why `test-mcpgo`/`test-officialsdk` exist as separate targets; never assume a root-level `./...` reached the adapters
- **Each adapter supports a RANGE of its MCP library, not just the pinned version**: `mcpgo` v0.53.0–v0.57.0, `officialsdk` v1.4.1–v1.7.0. `go.mod` pins the newest (what a fresh `go get` installs); the range is what the code tolerates when a user pins lower. Referencing a library symbol newer than the floor is a **compile error for those users**, and neither `make check` nor the main CI catches it — only the two compatibility workflows do. When a new library API is genuinely needed, reach it through `mcpgo/compat.go` / `officialsdk/compat.go`: **interface assertion** for methods (free, compile-safe everywhere), **cached-index reflection** for struct fields. Every shim degrades to the behavior that version would legitimately have had — absent, never wrong — and is pinned by a tripwire test in `compat_pinned_test.go` so an upstream rename fails a test instead of silently disabling telemetry. Those tripwires assert presence, so they are excluded on downgraded runs via the `mcpgo_legacy` / `gosdk_legacy` build tags, which are **test-only** and set solely by the compatibility matrices. Tests needing an API that no shim can stand in for (`mcp.InputRequestMap` literals, `server.WithInputSchemaValidation`) belong in a tagged file; tests needing a *capability* should skip at runtime on the capability instead
