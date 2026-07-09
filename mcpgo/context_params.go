package mcpgo

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	agentcat "go.agentcat.com/sdk"
)

const contextParamName = "context"

// addContextParamsToToolsList injects the context parameter into each tool in
// the list. customDescription overrides the default parameter description
// (agentcat.DefaultContextDescription) when non-empty.
func addContextParamsToToolsList(result *mcp.ListToolsResult, customDescription string) {
	if result == nil || len(result.Tools) == 0 {
		return
	}

	description := agentcat.ResolveContextDescription(customDescription)

	tools := make([]mcp.Tool, len(result.Tools))
	for i, tool := range result.Tools {
		tools[i] = ensureToolHasContextParam(tool, description)
	}

	result.Tools = tools
}

func ensureToolHasContextParam(tool mcp.Tool, description string) mcp.Tool {
	if toolHasContextParam(tool) {
		return tool
	}

	if len(tool.RawInputSchema) > 0 {
		if updatedSchema, ok := addContextParamToRawSchema(tool.RawInputSchema, description); ok {
			tool.RawInputSchema = updatedSchema
		}
		return tool
	}

	if tool.InputSchema.Type != "" && tool.InputSchema.Type != "object" {
		// Context is modelled as an object property; don't attempt to coerce other schema types
		return tool
	}

	props := copyStringAnyMap(tool.InputSchema.Properties)
	if props == nil {
		props = make(map[string]any, 1)
	}
	props[contextParamName] = map[string]any{
		"type":        "string",
		"description": description,
	}
	tool.InputSchema.Properties = props

	if tool.InputSchema.Type == "" {
		tool.InputSchema.Type = "object"
	}

	tool.InputSchema.Required = ensureStringPresent(tool.InputSchema.Required, contextParamName)

	return tool
}

func toolHasContextParam(tool mcp.Tool) bool {
	if tool.InputSchema.Properties != nil {
		if _, ok := tool.InputSchema.Properties[contextParamName]; ok {
			return true
		}
	}

	if len(tool.RawInputSchema) > 0 {
		var schema map[string]any
		if err := json.Unmarshal(tool.RawInputSchema, &schema); err == nil {
			if props, ok := schema["properties"].(map[string]any); ok {
				if _, exists := props[contextParamName]; exists {
					return true
				}
			}
		}
	}

	return false
}

func addContextParamToRawSchema(raw json.RawMessage, description string) (json.RawMessage, bool) {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return raw, false
	}

	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any, 1)
	}

	if _, exists := props[contextParamName]; exists {
		return raw, false
	}

	props[contextParamName] = map[string]any{
		"type":        "string",
		"description": description,
	}

	schema["properties"] = props

	if kind, ok := schema["type"].(string); !ok || kind == "" {
		schema["type"] = "object"
	}

	required := extractRequired(schema["required"])
	if !containsString(required, contextParamName) {
		required = append(required, contextParamName)
	}
	schema["required"] = required

	updated, err := json.Marshal(schema)
	if err != nil {
		return raw, false
	}

	return json.RawMessage(updated), true
}

func extractRequired(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return cloneStringSlice(v)
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return nil
	}
}

func copyStringAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func ensureStringPresent(in []string, candidate string) []string {
	required := cloneStringSlice(in)
	if containsString(required, candidate) {
		return required
	}

	return append(required, candidate)
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
