# Migrating from `github.com/mcpcat/mcpcat-go-sdk` to `go.agentcat.com/sdk`

MCPcat is now **AgentCat** — same team, same product, new name. The Go module has been renamed from `github.com/mcpcat/mcpcat-go-sdk` to [`go.agentcat.com/sdk`](https://pkg.go.dev/go.agentcat.com/sdk).

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

The adapter packages are still named `mcpgo` and `officialsdk`; only their import paths changed. The public API is unchanged — `Track()`, `Options`, `UserIdentity`, the `Identify` and redaction hooks, and the shutdown function all work exactly as before.

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

**Does `Track()` or any option behave differently?** The API surface is the same — `Track()`, `Options`, `UserIdentity`, and the hooks all work as before. Defaults were rebranded: events go to `api.agentcat.com` (the old endpoint still works via override), debug logs go to `~/agentcat.log`, and identify events are published as `agentcat:identify`. The new SDK also adds telemetry exporters (`otlp`, `datadog`, `sentry`, `posthog`) and a telemetry-only mode — see the README.

**Questions?** Open an issue or email [hi@agentcat.com](mailto:hi@agentcat.com).
