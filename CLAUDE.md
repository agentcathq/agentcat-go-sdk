# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

AgentCat Go SDK — an analytics agent that MCP server maintainers integrate to capture usage telemetry (tool calls, resource reads, prompt requests, sessions, user identity). It supports two MCP libraries via separate Go modules: `mcpgo/` (mark3labs/mcp-go) and `officialsdk/` (modelcontextprotocol/go-sdk).

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
go.work                     # workspace: root + mcpgo + officialsdk + examples
mcpcat.go / types.go        # root module — shared integration API & type aliases
mcpgo/                      # mcp-go adapter (separate go.mod, depends on mark3labs/mcp-go)
  mcpgo.go                  # Track() entry point, hooks-based integration
  hooks.go                  # MCP hook installation for event capture
  session.go                # session metadata extraction from mcp-go types
officialsdk/                # official go-sdk adapter (separate go.mod)
  officialsdk.go            # Track() entry point, middleware-based integration
  middleware.go             # receiving middleware for event capture
  session.go                # session metadata from go-sdk ServerSession
internal/                   # shared internals (only importable by root module)
  core/                     # types: Options, Event, Session, AgentCatInstance, UserIdentity
  publisher/                # async event publisher with worker pool → AgentCat API
  event/                    # event construction helpers
  redaction/                # applies RedactFunc to event fields before publish
  registry/                 # thread-safe server→AgentCatInstance map
  logging/                  # file-backed debug logger (~/agentcat.log)
  session/                  # session ID generation
  testutil/                 # shared test helpers
examples/                   # runnable examples for both adapters (basic + advanced)
```

## Architecture

**Three-module design**: The root module (`go.agentcat.com/sdk`) holds all shared logic in `internal/`. The two adapter modules (`mcpgo/` and `officialsdk/`) each have their own `go.mod` and depend on the root module plus their respective MCP library. Users import only the adapter matching their MCP library.

**Track() is the single entry point** in both adapters. It registers the server, initializes the global publisher singleton, and installs hooks (mcp-go) or receiving middleware (official go-sdk) for automatic event capture.

**Publisher** is a global singleton (`sync.Once`) with a buffered channel queue and worker pool. Events are enqueued via `Publish()`, redacted if configured, and sent to the AgentCat API. `Shutdown()` drains the queue with a context deadline (default 5s).

**Registry** maps server instances → `AgentCatInstance` (project ID + options) via a `sync.RWMutex`-guarded map. This lets hooks/middleware look up config for the server they're attached to.

**Two optional injected tools**: `get_more_tools` (lets LLMs report missing functionality) and a `context` parameter on existing tools (captures user intent). Both controlled via `Options`.

## Key Conventions

- Type aliases in `types.go` re-export `internal/core` types so adapters import from root, not internal
- Both adapters re-export `UserIdentity` so end users never import the root module directly
- Tests live alongside implementation (`_test.go` in same package); use table-driven tests
- Concurrency-sensitive code (registry, publisher, logging) needs `go test -race`
- CI runs `fmt-check`, `vet`, and `test` — all must pass before merge
