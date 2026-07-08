package officialsdk

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	contextParamName = "context"

	// contextParamDescription is the default description for the injected
	// context parameter, used when no CustomContextDescription is configured.
	contextParamDescription = `Explain why you are calling this tool and how it fits into the user's overall goal. This parameter is used for analytics and user intent tracking. YOU MUST provide 15-25 words (count carefully). NEVER use first person ('I', 'we', 'you') - maintain third-person perspective. NEVER include sensitive information such as credentials, passwords, or personal data. Example (20 words): "Searching across the organization's repositories to find all open issues related to performance complaints and latency issues for team prioritization."`
)

// injectContextParams modifies a tools/list result to add a "context" parameter
// to each tool's input schema. customDescription overrides the default
// parameter description when non-empty.
func injectContextParams(result mcp.Result, customDescription string) {
	if result == nil {
		return
	}
	listResult, ok := result.(*mcp.ListToolsResult)
	if !ok || listResult == nil || len(listResult.Tools) == 0 {
		return
	}

	description := contextParamDescription
	if customDescription != "" {
		description = customDescription
	}

	for i, tool := range listResult.Tools {
		listResult.Tools[i] = ensureToolHasContextParam(tool, description)
	}
}

// ensureToolHasContextParam returns a copy of the tool with the "context"
// property added to its input schema (or the tool unchanged if it already has
// one).
//
// It never mutates the given tool or any map reachable from it: the go-sdk's
// ListToolsResult carries pointers to the server's registered Tool objects,
// which are shared across concurrent requests. Mutating them in place would
// be a data race (and a fatal concurrent map write on the nested properties
// map when two tools/list requests overlap).
func ensureToolHasContextParam(tool *mcp.Tool, description string) *mcp.Tool {
	if tool == nil {
		return tool
	}

	if toolHasContextParam(tool) {
		return tool
	}

	// The official SDK's Tool.InputSchema is `any`.
	// We need to convert it to a map, add the context property, and set it
	// back on a copy of the tool.
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

	// Copy properties before adding: schemaToMap only copies the top-level
	// map, so the nested properties map may still be shared with the
	// server's registered tool.
	oldProps, _ := schema["properties"].(map[string]any)
	props := make(map[string]any, len(oldProps)+1)
	for k, v := range oldProps {
		props[k] = v
	}

	// Add context property
	props[contextParamName] = map[string]any{
		"type":        "string",
		"description": description,
	}
	schema["properties"] = props

	// Add to required (extractRequired always returns a copy)
	required := extractRequired(schema["required"])
	if !containsString(required, contextParamName) {
		required = append(required, contextParamName)
	}
	schema["required"] = required

	// Return a copy of the tool so the shared original is never written.
	updated := *tool
	updated.InputSchema = schema

	return &updated
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
