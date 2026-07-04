package officialsdk

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	contextParamName        = "context"
	contextParamDescription = `Explain why you are calling this tool and how it fits into the user's overall goal. This parameter is used for analytics and user intent tracking. YOU MUST provide 15-25 words (count carefully). NEVER use first person ('I', 'we', 'you') - maintain third-person perspective. NEVER include sensitive information such as credentials, passwords, or personal data. Example (20 words): "Searching across the organization's repositories to find all open issues related to performance complaints and latency issues for team prioritization."`
)

// injectContextParams modifies a tools/list result to add a "context" parameter
// to each tool's input schema.
func injectContextParams(result mcp.Result) {
	if result == nil {
		return
	}
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil || len(listResult.Tools) == 0 {
		return
	}

	for i, tool := range listResult.Tools {
		listResult.Tools[i] = ensureToolHasContextParam(tool)
	}
}

// ensureToolHasContextParam adds the "context" property to a tool's input schema
// if it doesn't already exist.
func ensureToolHasContextParam(tool *mcp.Tool) *mcp.Tool {
	if tool == nil {
		return tool
	}

	if toolHasContextParam(tool) {
		return tool
	}

	// The official SDK's Tool.InputSchema is `any`.
	// We need to convert it to a map, add the context property, and set it back.
	schema := schemaToMap(tool.InputSchema)
	if schema == nil {
		schema = map[string]any{
			"type": "object",
		}
	}

	// Don't modify non-object schemas
	if schemaType, ok := schema["type"].(string); ok && schemaType != "" && schemaType != "object" {
		return tool
	}

	// Ensure type is "object"
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}

	// Get or create properties
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}

	// Add context property
	props[contextParamName] = map[string]any{
		"type":        "string",
		"description": contextParamDescription,
	}
	schema["properties"] = props

	// Add to required
	required := extractRequired(schema["required"])
	if !containsString(required, contextParamName) {
		required = append(required, contextParamName)
	}
	schema["required"] = required

	tool.InputSchema = schema

	return tool
}

// toolHasContextParam checks if the tool already has a "context" parameter.
func toolHasContextParam(tool *mcp.Tool) bool {
	if tool == nil || tool.InputSchema == nil {
		return false
	}

	schema := schemaToMap(tool.InputSchema)
	if schema == nil {
		return false
	}

	if props, ok := schema["properties"].(map[string]any); ok {
		if _, exists := props[contextParamName]; exists {
			return true
		}
	}

	return false
}

// schemaToMap converts a Tool's InputSchema (which is `any`) to map[string]any.
// It handles the case where InputSchema is already a map or needs JSON
// round-tripping.
func schemaToMap(schema any) map[string]any {
	if schema == nil {
		return nil
	}

	// If it's already a map[string]any, return a copy
	if m, ok := schema.(map[string]any); ok {
		result := make(map[string]any, len(m))
		for k, v := range m {
			result[k] = v
		}
		return result
	}

	// Otherwise, JSON round-trip
	data, err := json.Marshal(schema)
	if err != nil {
		return nil
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

// extractRequired extracts the "required" array from a schema value.
func extractRequired(raw any) []string {
	switch v := raw.(type) {
	case []string:
		result := make([]string, len(v))
		copy(result, v)
		return result
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

// containsString checks if a string is present in a slice.
func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
