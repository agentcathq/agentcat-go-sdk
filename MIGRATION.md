# Migrating from `github.com/mcpcat/mcpcat-go-sdk` to `go.agentcat.com/sdk`

MCPcat is now **AgentCat** — same team, same product, new name. The Go module has been renamed from `github.com/mcpcat/mcpcat-go-sdk` to [`go.agentcat.com/sdk`](https://pkg.go.dev/go.agentcat.com/sdk).

## Nothing breaks if you stay

We keep every existing surface alive **permanently** — not on a deprecation timer:

- The `github.com/mcpcat/mcpcat-go-sdk` module stays published and is **not retracted** — existing `go.mod` pins keep resolving and building forever
- `api.mcpcat.io` keeps accepting events forever
- The `MCPCAT_API_URL` environment variable keeps working (it is still the name the new SDK reads)
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

**Unchanged on purpose (for compatibility):**

- The `MCPCAT_API_URL` environment variable — still the override the SDK reads
- The default endpoint `https://api.mcpcat.io`
- The debug log file `~/mcpcat.log`
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
5. Do NOT rename the MCPCAT_API_URL environment variable, the ~/mcpcat.log path, or any api.mcpcat.io endpoint override — the new SDK still uses these names.
6. Run `go mod tidy`, then the project's build and tests to verify, and report anything referencing mcpcat that could not be migrated mechanically.
```

## FAQ

**Do I have to migrate?** No — and there is no deadline. The old module and endpoint stay up permanently.

**Will my data/history split?** No. Both SDKs report into the same platform and your history stays unified under your project.

**Does `Track()` or any option behave differently?** No. The rename is import-path and package-name only; behavior, options, and defaults are identical.

**Questions?** Open an issue or email [hi@agentcat.com](mailto:hi@agentcat.com).
