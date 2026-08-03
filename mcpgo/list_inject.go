package mcpgo

import (
	"context"
	"encoding/json"
	"weak"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// Config derivation is shared: agentcat.BuildInjectConfig(instance.Options,
// hookMode). Do not duplicate it here.

// normalizeMCPGoTools converts listed tools to the engine's neutral form.
// mcp-go tools carry either a structured InputSchema or RawInputSchema;
// marshaling the whole tool's schema handles both.
//
// A schema this SDK cannot read is marked OPAQUE rather than dropped to nil:
// nil would read as "declares no schema", and the pipeline would then be free
// to write one — replacing a contract the customer really did declare (a
// legal `true` schema, say, which accepts anything) with AgentCat's guess.
// Opaque tools are advertised exactly as the customer wrote them.
func normalizeMCPGoTools(tools []mcp.Tool) []agentcat.NormalizedTool {
	out := make([]agentcat.NormalizedTool, 0, len(tools))
	for _, t := range tools {
		nt := agentcat.NormalizedTool{Name: t.Name}
		inRaw := t.RawInputSchema
		if inRaw == nil {
			b, err := json.Marshal(t.InputSchema)
			if err != nil {
				nt.InputSchemaOpaque = true
			} else {
				inRaw = b
			}
		}
		if inRaw != nil {
			if so, err := agentcat.ParseSchemaObject(inRaw); err == nil {
				nt.InputSchema = so
			} else {
				agentcat.LogWarn("mcpgo: tool %q has an input schema this SDK cannot parse; advertising it untouched: %v", t.Name, err)
				nt.InputSchemaOpaque = true
			}
		}
		outRaw := t.RawOutputSchema
		if outRaw == nil && t.OutputSchema.Type != "" {
			b, err := json.Marshal(t.OutputSchema)
			if err != nil {
				nt.OutputSchemaOpaque = true
			} else {
				outRaw = b
			}
		}
		if outRaw != nil {
			if so, err := agentcat.ParseSchemaObject(outRaw); err == nil {
				nt.OutputSchema = so
			} else {
				agentcat.LogWarn("mcpgo: tool %q has an output schema this SDK cannot parse; advertising it untouched: %v", t.Name, err)
				nt.OutputSchemaOpaque = true
			}
		}
		out = append(out, nt)
	}
	return out
}

// applyInjectedToolsMCPGo writes mutated schemas back as RawInputSchema /
// RawOutputSchema on the listed tools. mcp-go hands the hook value copies, so
// the server's registered tools are never touched — but their Properties maps
// are shared, so the structured fields are replaced wholesale rather than
// edited (mcp-go also forbids mixing structured and raw schemas on one tool).
//
// A tool whose schema the engine could not read is skipped entirely: writing
// anything back over it would destroy the customer's declared contract.
func applyInjectedToolsMCPGo(result *mcp.ListToolsResult, injected []agentcat.NormalizedTool) {
	byName := make(map[string]agentcat.NormalizedTool, len(injected))
	for _, nt := range injected {
		byName[nt.Name] = nt
	}
	for i := range result.Tools {
		nt, ok := byName[result.Tools[i].Name]
		if !ok {
			continue
		}
		if nt.InputSchema != nil && !nt.InputSchemaOpaque {
			if raw, err := json.Marshal(nt.InputSchema); err == nil {
				result.Tools[i].InputSchema = mcp.ToolInputSchema{}
				result.Tools[i].RawInputSchema = raw
			}
		}
		if nt.OutputSchema != nil && !nt.OutputSchemaOpaque {
			if raw, err := json.Marshal(nt.OutputSchema); err == nil {
				result.Tools[i].OutputSchema = mcp.ToolOutputSchema{}
				result.Tools[i].RawOutputSchema = raw
			}
		}
	}
}

// stashRebuild wires the instance's rebuild function so a tools/call landing
// on an instance that never served tools/list can still get registries: it
// drives a synthetic tools/list through the server, and the list hook above
// stores the registries as a side effect (hence the nil tool list return).
//
// mcp-go exposes no inner list handler to call directly — unlike the official
// SDK's middleware chain. MCPServer.ListTools() would return the registered
// tools, but only the server's own message entry point honours tool filters
// and session-scoped tools, which is what the advertised list depends on. It
// runs once per instance until a real tools/list arrives; the customer's own
// hooks observe that one synthetic request.
func stashRebuild(instance *agentcat.AgentCatInstance, mcpServer *server.MCPServer) {
	if instance == nil || mcpServer == nil {
		return
	}
	weakServer := weak.Make(mcpServer)
	instance.RebuildTools = func(ctx context.Context, _ any) ([]agentcat.NormalizedTool, error) {
		live := weakServer.Value()
		if live == nil {
			return nil, nil
		}
		raw := json.RawMessage(`{"jsonrpc":"2.0","id":"agentcat-rebuild","method":"tools/list","params":{}}`)
		_ = live.HandleMessage(ctx, raw)
		return nil, nil
	}
}

// registerListInjection wires the AfterListTools hook that runs the pure
// pipeline and stores the registries on the instance.
func registerListInjection(hooks *server.Hooks, mcpServer *server.MCPServer, hookMode bool) {
	hooks.AddAfterListTools(func(ctx context.Context, id any, message *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
		// Hooks run synchronously inside mcp-go's request handling, which has
		// no panic recovery of its own: recover, log, and advertise the
		// untouched list rather than crashing the customer's server.
		defer func() {
			if r := recover(); r != nil {
				agentcat.LogRecoveredPanic("mcpgo list injection", r)
			}
		}()
		// A customer may pass one *server.Hooks value to several servers
		// (server.WithHooks), so this callback can fire for a server it was
		// not registered for. hookMode and the instance below belong to
		// mcpServer alone, so stand down for anyone else's request.
		if server.ServerFromContext(ctx) != mcpServer {
			return
		}
		instance := agentcat.GetInstance(mcpServer)
		if instance == nil || instance.Options == nil || result == nil {
			return
		}
		cfg := agentcat.BuildInjectConfig(instance.Options, hookMode)
		normalized := normalizeMCPGoTools(result.Tools)
		injected, reg := agentcat.BuildInjectedTools(cfg, normalized)
		applyInjectedToolsMCPGo(result, injected)
		// MERGE, never replace: this list may be one page of a paginated
		// result, or a filtered or session-scoped subset. Tools it does not
		// name must keep the entries an earlier list recorded for them, or
		// their injected arguments stop being stripped.
		instance.MergeRegistries(reg)
		// Report any session_id the customer declared themselves: this list
		// is where the collision becomes known, and it costs them correlation.
		agentcat.ReportSessionParamCollisions(instance, reg)
		// Tools registered after Track get AgentCat's properties declared on
		// their registered schemas here, before any call can arrive: an agent
		// can only call a tool it has listed. Session-scoped tools are only
		// reachable from here, where the session is in context.
		declareRegisteredSchemas(mcpServer, cfg)
		declareSessionSchemas(ctx, mcpServer, cfg)
	})
}
