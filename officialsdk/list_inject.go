package officialsdk

import (
	"context"
	"encoding/json"
	"weak"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// Config derivation is shared: agentcat.BuildInjectConfig(instance.Options,
// hookMode). Do not duplicate it here.

// normalizeOfficialTools converts listed tools into the engine's neutral
// form. The JSON round trip doubles as the mandatory deep copy: listTools
// returns pointers to the server's live registered *mcp.Tool objects.
//
// A schema this SDK cannot read is marked OPAQUE rather than dropped to nil:
// nil would read as "declares no schema", and the pipeline would then be free
// to write one — replacing a contract the customer really did declare with
// AgentCat's guess. Opaque tools are advertised exactly as they were written.
func normalizeOfficialTools(tools []*mcp.Tool) []agentcat.NormalizedTool {
	out := make([]agentcat.NormalizedTool, 0, len(tools))
	for _, t := range tools {
		nt := agentcat.NormalizedTool{Name: t.Name}
		if t.InputSchema != nil {
			raw, err := json.Marshal(t.InputSchema)
			if err != nil {
				agentcat.LogWarn("officialsdk: tool %q has an input schema this SDK cannot marshal; advertising it untouched: %v", t.Name, err)
				nt.InputSchemaOpaque = true
			} else if so, err := agentcat.ParseSchemaObject(raw); err == nil {
				nt.InputSchema = so
			} else {
				agentcat.LogWarn("officialsdk: tool %q has an input schema this SDK cannot parse; advertising it untouched: %v", t.Name, err)
				nt.InputSchemaOpaque = true
			}
		}
		if t.OutputSchema != nil {
			raw, err := json.Marshal(t.OutputSchema)
			if err != nil {
				agentcat.LogWarn("officialsdk: tool %q has an output schema this SDK cannot marshal; advertising it untouched: %v", t.Name, err)
				nt.OutputSchemaOpaque = true
			} else if so, err := agentcat.ParseSchemaObject(raw); err == nil {
				nt.OutputSchema = so
			} else {
				agentcat.LogWarn("officialsdk: tool %q has an output schema this SDK cannot parse; advertising it untouched: %v", t.Name, err)
				nt.OutputSchemaOpaque = true
			}
		}
		out = append(out, nt)
	}
	return out
}

// applyInjectedTools writes the engine's mutated schemas back onto COPIES of
// the listed tools; the server's registered tools are never touched.
//
// A tool whose schema the engine could not read is left exactly as the
// customer wrote it: writing anything back over it would destroy their
// declared contract.
func applyInjectedTools(result *mcp.ListToolsResult, injected []agentcat.NormalizedTool) {
	byName := make(map[string]agentcat.NormalizedTool, len(injected))
	for _, nt := range injected {
		byName[nt.Name] = nt
	}
	newTools := make([]*mcp.Tool, len(result.Tools))
	for i, t := range result.Tools {
		cp := *t
		if nt, ok := byName[t.Name]; ok {
			if nt.InputSchema != nil && !nt.InputSchemaOpaque {
				cp.InputSchema = nt.InputSchema // SchemaObject marshals in order
			}
			if nt.OutputSchema != nil && !nt.OutputSchemaOpaque {
				cp.OutputSchema = nt.OutputSchema
			}
		}
		newTools[i] = &cp
	}
	result.Tools = newTools
}

// handleToolsList runs the injection pipeline on a tools/list result and
// folds the registries into the instance. Panic-safe wrapper is the caller.
func handleToolsList(result mcp.Result, instance *agentcat.AgentCatInstance, cfg agentcat.InjectConfig) {
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || instance == nil {
		return
	}
	normalized := normalizeOfficialTools(listResult.Tools)
	injected, reg := agentcat.BuildInjectedTools(cfg, normalized)
	applyInjectedTools(listResult, injected)
	// MERGE, never replace: with ServerOptions.PageSize set, this list is one
	// page. Tools it does not name must keep the entries an earlier page
	// recorded for them, or their injected arguments stop being stripped.
	instance.MergeRegistries(reg)

	// Report any session_id the customer declared themselves: this list is
	// where the collision becomes known, and it costs them correlation.
	agentcat.ReportSessionParamCollisions(instance, reg)
}

// rebuildTarget owns the composed inner handler chain for exactly the
// server's lifetime: the per-server middleware handler references it strongly
// (every dispatch reads target.next), and RebuildTools references it only
// weakly. The global registry holds every instance strongly, so a strong
// RebuildTools→next reference would keep alive whatever the chain's closures
// capture — customer middleware routinely captures the server, which in the
// per-request factory topology pinned one server per request forever. A
// collected server takes the holder — and everything those closures capture —
// down with it, mirroring the weak server in mcpgo's stashRebuild.
type rebuildTarget struct {
	next mcp.MethodHandler
}

// stashRebuild wires the instance's rebuild function to the holder of the
// inner (already composed) next handler, so a tools/call landing on a fresh
// instance can re-derive registries by fetching the ORIGINAL uninjected list.
func stashRebuild(instance *agentcat.AgentCatInstance, target *rebuildTarget) {
	if instance == nil || target == nil {
		return
	}
	weakTarget := weak.Make(target)
	instance.RebuildTools = func(ctx context.Context, session any) ([]agentcat.NormalizedTool, error) {
		live := weakTarget.Value()
		if live == nil {
			return nil, nil // chain (and server) collected: stand down
		}
		ss, _ := session.(*mcp.ServerSession)
		req := &mcp.ListToolsRequest{Session: ss, Params: &mcp.ListToolsParams{}}
		res, err := live.next(ctx, "tools/list", req)
		if err != nil {
			return nil, err
		}
		listResult, ok := res.(*mcp.ListToolsResult)
		if !ok {
			return nil, nil
		}
		return normalizeOfficialTools(listResult.Tools), nil
	}
}
