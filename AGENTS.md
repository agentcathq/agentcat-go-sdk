# Repository Guidelines

## Project Structure & Module Organization
This Go module exposes the public SDK in `mcpcat.go` and `types.go`. Library-specific state lives under `internal/`. Key packages: `internal/compat` detects supported MCP servers and applies server options; `internal/tracking` registers Mark3 Labs hooks and cooperates with `internal/session` to capture client metadata; `internal/logging` centralizes file-backed logging to `~/mcpcat.log`; `internal/registry` keeps a thread-safe map between servers and `MCPcatInstance`. `internal/event` and `internal/redaction` currently stub future expansion—keep contributions narrowly scoped and documented.

## Build, Test, and Development Commands
Use `go build ./...` to confirm every package compiles before opening a PR. Run `go test ./...` (add `-run <Pattern>` for focused suites, `-cover` for coverage) once you introduce `_test.go` files. Format code with `go fmt ./...`; pair it with `gofmt -w <file>` for quick fixes. Apply `go mod tidy` only when dependencies truly change so the module file stays stable.

## Coding Style & Naming Conventions
Follow default Go style: tabs for indentation, `UpperCamelCase` for exported symbols, `lowerCamelCase` for private helpers, and all-cap prefixes like `PrefixEvent` only for immutable constants. Keep package-level state guarded by `sync` primitives as seen in `internal/registry`. Document non-obvious behavior with short comments next to the relevant block rather than above every line.

## Testing Guidelines
Place tests beside implementation packages (e.g., `internal/session/session_test.go`). Mirror exported APIs with table-driven tests, explicitly covering concurrent access paths (`registry`, `logging`) and edge cases such as nil servers. Prefer deterministic clocks/mocks over real time by injecting dependencies where necessary. Run `go test -race ./internal/...` when touching shared-memory code to guard concurrency regressions.

## Commit & Pull Request Guidelines
The existing history is brief (`FIRST CUT`), so set the tone with clear, imperative commit subjects capped at ~70 characters (e.g., `Add Mark3Labs hook instrumentation`). Reference related issues in the body and call out TODO resolutions. PRs should link to context, explain behavioral impact, list manual verification (e.g., `go test ./...`), and include screenshots only when user-facing behavior changes.
