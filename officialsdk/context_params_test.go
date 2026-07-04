package officialsdk

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestInjectContextParams_NilResult(t *testing.T) {
	// Should not panic
	injectContextParams(nil)
}

func TestInjectContextParams_NonListToolsResult(t *testing.T) {
	// Should handle non-ListToolsResult gracefully
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "hello"},
		},
	}
	// Should not panic
	injectContextParams(result)
}

func TestInjectContextParams_EmptyToolsList(t *testing.T) {
	result := &mcp.ListToolsResult{Tools: []*mcp.Tool{}}
	injectContextParams(result)
	if len(result.Tools) != 0 {
		t.Errorf("expected empty tools array, got %d tools", len(result.Tools))
	}
}

func TestInjectContextParams_SingleTool(t *testing.T) {
	result := &mcp.ListToolsResult{
		Tools: []*mcp.Tool{
			{
				Name:        "test_tool",
				Description: "A test tool",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
	}

	injectContextParams(result)

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}

	schema, ok := result.Tools[0].InputSchema.(map[string]any)
	if !ok {
		t.Fatal("expected InputSchema to be map[string]any")
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be map[string]any")
	}

	if _, exists := props[contextParamName]; !exists {
		t.Error("context param was not added")
	}

	required := extractRequired(schema["required"])
	if !containsString(required, contextParamName) {
		t.Error("context param not in required array")
	}
}

func TestInjectContextParams_MultipleTool(t *testing.T) {
	result := &mcp.ListToolsResult{
		Tools: []*mcp.Tool{
			{
				Name:        "tool1",
				Description: "First tool",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
			{
				Name:        "tool2",
				Description: "Second tool",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
					"required": []any{"query"},
				},
			},
		},
	}

	injectContextParams(result)

	if len(result.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result.Tools))
	}

	for i, tool := range result.Tools {
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("tool %d: expected InputSchema to be map[string]any", i)
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("tool %d: expected properties to be map[string]any", i)
		}
		if _, exists := props[contextParamName]; !exists {
			t.Errorf("tool %d: context param was not added", i)
		}
	}

	// Verify tool2 still has query in required along with context
	schema2 := result.Tools[1].InputSchema.(map[string]any)
	required := extractRequired(schema2["required"])
	if !containsString(required, "query") {
		t.Error("tool2: original 'query' required field was lost")
	}
	if !containsString(required, contextParamName) {
		t.Error("tool2: context not added to required")
	}
}

func TestInjectContextParams_AlreadyHasContext(t *testing.T) {
	result := &mcp.ListToolsResult{
		Tools: []*mcp.Tool{
			{
				Name:        "tool_with_context",
				Description: "A tool that already has context",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"context": map[string]any{
							"type":        "string",
							"description": "My custom context param",
						},
					},
					"required": []any{"context"},
				},
			},
		},
	}

	injectContextParams(result)

	schema := result.Tools[0].InputSchema.(map[string]any)
	props := schema["properties"].(map[string]any)

	// Should still have context
	contextProp := props[contextParamName].(map[string]any)
	// Should NOT overwrite the existing description
	if contextProp["description"] != "My custom context param" {
		t.Error("existing context param description was overwritten")
	}
}

func TestInjectContextParams_NoTypeProperty(t *testing.T) {
	// Schema without explicit "type" field
	result := &mcp.ListToolsResult{
		Tools: []*mcp.Tool{
			{
				Name:        "no_type_tool",
				Description: "Tool without type in schema",
				InputSchema: map[string]any{
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	injectContextParams(result)

	schema := result.Tools[0].InputSchema.(map[string]any)

	// Should set type to "object"
	if schema["type"] != "object" {
		t.Errorf("expected type to be set to 'object', got %v", schema["type"])
	}

	// Should have context
	props := schema["properties"].(map[string]any)
	if _, exists := props[contextParamName]; !exists {
		t.Error("context param was not added to schema without type")
	}
}

func TestInjectContextParams_NilInputSchema(t *testing.T) {
	result := &mcp.ListToolsResult{
		Tools: []*mcp.Tool{
			{
				Name:        "nil_schema_tool",
				Description: "Tool with nil InputSchema",
				InputSchema: nil,
			},
		},
	}

	injectContextParams(result)

	schema, ok := result.Tools[0].InputSchema.(map[string]any)
	if !ok {
		t.Fatal("expected InputSchema to be set to a map[string]any")
	}

	if schema["type"] != "object" {
		t.Errorf("expected type 'object', got %v", schema["type"])
	}

	props := schema["properties"].(map[string]any)
	if _, exists := props[contextParamName]; !exists {
		t.Error("context param was not added")
	}
}

func TestToolHasContextParam_WithContext(t *testing.T) {
	tool := &mcp.Tool{
		Name: "test",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"context": map[string]any{"type": "string"},
			},
		},
	}

	if !toolHasContextParam(tool) {
		t.Error("expected toolHasContextParam to return true")
	}
}

func TestToolHasContextParam_WithoutContext(t *testing.T) {
	tool := &mcp.Tool{
		Name: "test",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		},
	}

	if toolHasContextParam(tool) {
		t.Error("expected toolHasContextParam to return false")
	}
}

func TestToolHasContextParam_NilTool(t *testing.T) {
	if toolHasContextParam(nil) {
		t.Error("expected toolHasContextParam to return false for nil tool")
	}
}

func TestToolHasContextParam_NilSchema(t *testing.T) {
	tool := &mcp.Tool{
		Name:        "test",
		InputSchema: nil,
	}
	if toolHasContextParam(tool) {
		t.Error("expected toolHasContextParam to return false for nil schema")
	}
}

func TestSchemaToMap_NilSchema(t *testing.T) {
	result := schemaToMap(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestSchemaToMap_MapInput(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
	}

	result := schemaToMap(input)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["type"] != "object" {
		t.Errorf("expected type 'object', got %v", result["type"])
	}
}

func TestExtractRequired_StringSlice(t *testing.T) {
	result := extractRequired([]string{"a", "b", "c"})
	if len(result) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(result))
	}
	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestExtractRequired_AnySlice(t *testing.T) {
	result := extractRequired([]any{"a", "b", 42})
	if len(result) != 2 {
		t.Fatalf("expected 2 string elements, got %d", len(result))
	}
	if result[0] != "a" || result[1] != "b" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestExtractRequired_Nil(t *testing.T) {
	result := extractRequired(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestContainsString(t *testing.T) {
	if !containsString([]string{"a", "b", "c"}, "b") {
		t.Error("expected to find 'b'")
	}
	if containsString([]string{"a", "b", "c"}, "d") {
		t.Error("should not find 'd'")
	}
	if containsString(nil, "a") {
		t.Error("should not find anything in nil slice")
	}
}
