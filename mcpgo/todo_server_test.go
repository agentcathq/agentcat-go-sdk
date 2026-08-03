package mcpgo

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Todo represents a todo item
type Todo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"`
}

// TodoStore manages todo items in memory
type TodoStore struct {
	mu     sync.RWMutex
	todos  map[string]*Todo
	nextID int
}

// NewTodoStore creates a new todo store
func NewTodoStore() *TodoStore {
	return &TodoStore{
		todos:  make(map[string]*Todo),
		nextID: 1,
	}
}

// Add adds a new todo
func (s *TodoStore) Add(title, description string) *Todo {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := fmt.Sprintf("todo_%d", s.nextID)
	s.nextID++

	todo := &Todo{
		ID:          id,
		Title:       title,
		Description: description,
		Completed:   false,
	}
	s.todos[id] = todo
	return todo
}

// Get retrieves a todo by ID
func (s *TodoStore) Get(id string) (*Todo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	todo, ok := s.todos[id]
	if !ok {
		return nil, fmt.Errorf("todo not found: %s", id)
	}
	return todo, nil
}

// List returns all todos
func (s *TodoStore) List() []*Todo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Todo, 0, len(s.todos))
	for _, todo := range s.todos {
		result = append(result, todo)
	}
	return result
}

// Complete marks a todo as completed
func (s *TodoStore) Complete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	todo, ok := s.todos[id]
	if !ok {
		return fmt.Errorf("todo not found: %s", id)
	}
	todo.Completed = true
	return nil
}

// Delete removes a todo
func (s *TodoStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.todos[id]; !ok {
		return fmt.Errorf("todo not found: %s", id)
	}
	delete(s.todos, id)
	return nil
}

// CreateTodoServerSimple creates an MCP server with todo app tools. Extra
// server options (e.g. server.WithHooks for customer hooks) are applied after
// the defaults, mirroring how a customer builds their own server.
func CreateTodoServerSimple(opts ...server.ServerOption) (*server.MCPServer, *TodoStore) {
	mcpServer := server.NewMCPServer(
		"todo-server",
		"1.0.0",
		append([]server.ServerOption{server.WithToolCapabilities(true)}, opts...)...,
	)

	store := NewTodoStore()
	registerTodoTools(mcpServer, store)
	return mcpServer, store
}

// CreateFullServer creates an MCP server with tools, resources, and prompts.
// This provides the full MCP surface area for integration testing.
func CreateFullServer() (*server.MCPServer, *TodoStore) {
	mcpServer := server.NewMCPServer(
		"todo-server",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, false),
		server.WithPromptCapabilities(true),
	)

	store := NewTodoStore()
	registerTodoTools(mcpServer, store)

	// Register the "about" resource
	aboutResource := mcp.NewResource(
		"todo://about",
		"about",
		mcp.WithResourceDescription("About this todo server"),
		mcp.WithMIMEType("text/plain"),
	)
	mcpServer.AddResource(aboutResource, func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "todo://about",
				MIMEType: "text/plain",
				Text:     "This is a simple todo server for integration testing.",
			},
		}, nil
	})

	// Register the "summarize_todos" prompt
	summarizePrompt := mcp.NewPrompt(
		"summarize_todos",
		mcp.WithPromptDescription("Summarize all current todos"),
		mcp.WithArgument("style", mcp.ArgumentDescription("The summary style, e.g. brief or detailed")),
	)
	mcpServer.AddPrompt(summarizePrompt, func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		style := request.Params.Arguments["style"]
		if style == "" {
			style = "brief"
		}

		todos := store.List()
		summary := fmt.Sprintf("Summarize the following %d todos in a %s style:\n", len(todos), style)
		for _, todo := range todos {
			status := "incomplete"
			if todo.Completed {
				status = "complete"
			}
			summary += fmt.Sprintf("- %s (%s): %s\n", todo.Title, status, todo.Description)
		}

		return &mcp.GetPromptResult{
			Description: "Summary of all todos",
			Messages: []mcp.PromptMessage{
				mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(summary)),
			},
		}, nil
	})

	return mcpServer, store
}

// orderedToolRawSchema is a customer-authored raw input schema whose property
// order is deliberately non-alphabetical. Registered via RawInputSchema so the
// bytes on the wire are the customer's own: injected params must be appended
// after them, and their relative order must survive untouched.
const orderedToolRawSchema = `{"type":"object","properties":{"zebra":{"type":"string","description":"Last alphabetically, first on the wire"},"apple":{"type":"string","description":"First alphabetically, second on the wire"}},"required":["zebra"]}`

// registerOrderedTool adds the ordered_tool fixture to an existing server.
func registerOrderedTool(mcpServer *server.MCPServer) {
	tool := mcp.NewToolWithRawSchema(
		"ordered_tool",
		"A tool whose raw input schema pins its property order",
		json.RawMessage(orderedToolRawSchema),
	)

	mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ordered"), nil
	})
}

// structuredOnlyOutput is the declared output shape of the structured_only
// fixture tool. A declared output schema is what gates the handle mirror.
type structuredOnlyOutput struct {
	Text string `json:"text"`
}

// registerHandleFixtures adds the tools the v2 call-path tests need:
//   - always_fails: an IsError result (mint-back must still be appended)
//   - echo_args: echoes the arguments the handler actually received, and
//     declares no output schema (so the mirror must stay off)
//   - structured_only: structuredContent and NO content blocks, with a
//     declared output schema (mint-back + mirror)
func registerHandleFixtures(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool("always_fails", mcp.WithDescription("Always returns an error result")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("intentional failure"), nil
		},
	)

	mcpServer.AddTool(
		mcp.NewTool("echo_args",
			mcp.WithDescription("Echoes the arguments the handler received"),
			mcp.WithString("payload", mcp.Description("Anything at all")),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := request.GetArguments()
			if args == nil {
				args = map[string]any{}
			}
			encoded, err := json.Marshal(args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(encoded)), nil
		},
	)

	mcpServer.AddTool(
		mcp.NewTool("pointer_error", mcp.WithDescription("Returns an error result with pointer content")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Both mcp.TextContent and *mcp.TextContent satisfy mcp.Content.
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Type: "text", Text: "pointer failure"}},
			}, nil
		},
	)

	mcpServer.AddTool(
		mcp.NewTool("slow_tool", mcp.WithDescription("Takes a measurable amount of time to run")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			time.Sleep(25 * time.Millisecond)
			return mcp.NewToolResultText("slow"), nil
		},
	)

	mcpServer.AddTool(
		mcp.NewTool("multi_error", mcp.WithDescription("Returns an error result built from several text blocks")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					mcp.NewTextContent("Error:"),
					mcp.NewTextContent("Invalid parameter."),
					mcp.NewTextContent("Expected string."),
				},
			}, nil
		},
	)

	mcpServer.AddTool(
		mcp.NewToolWithRawSchema("echo_raw",
			"Echoes the raw argument bytes the handler received",
			json.RawMessage(`{"type":"object"}`),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(string(request.Params.RawArguments)), nil
		},
	)

	mcpServer.AddTool(
		mcp.NewTool("big_numbers",
			mcp.WithDescription("Returns structuredContent with integers beyond float64 precision"),
			mcp.WithRawOutputSchema(json.RawMessage(`{"type":"object"}`)),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{mcp.NewTextContent("big")},
				StructuredContent: json.RawMessage(
					`{"exact":9007199254740993,"id":1234567890123456789,"nested":{"huge":12345678901234567890},"ratio":1.5}`,
				),
			}, nil
		},
	)

	mcpServer.AddTool(
		mcp.NewTool("structured_no_schema",
			mcp.WithDescription("Returns structuredContent without declaring an output schema"),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent("plain")},
				StructuredContent: map[string]any{"text": "plain"},
			}, nil
		},
	)

	mcpServer.AddTool(
		mcp.NewTool("structured_only",
			mcp.WithDescription("Returns structuredContent and no content blocks"),
			mcp.WithOutputSchema[structuredOnlyOutput](),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				StructuredContent: map[string]any{"text": "structured"},
			}, nil
		},
	)
}

func registerTodoTools(mcpServer *server.MCPServer, store *TodoStore) {

	// Add todo tool
	addTodoTool := mcp.NewTool(
		"add_todo",
		mcp.WithDescription("Add a new todo item"),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Title of the todo"),
		),
		mcp.WithString("description",
			mcp.Description("Description of the todo"),
		),
	)

	mcpServer.AddTool(addTodoTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title, err := request.RequireString("title")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		description := request.GetString("description", "")

		todo := store.Add(title, description)
		return mcp.NewToolResultText(fmt.Sprintf("Added todo: %s (ID: %s)", todo.Title, todo.ID)), nil
	})

	// List todos tool
	listTodosTool := mcp.NewTool(
		"list_todos",
		mcp.WithDescription("List all todo items"),
	)

	mcpServer.AddTool(listTodosTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		todos := store.List()
		if len(todos) == 0 {
			return mcp.NewToolResultText("No todos found"), nil
		}

		result := "Todos:\n"
		for _, todo := range todos {
			status := "[ ]"
			if todo.Completed {
				status = "[x]"
			}
			result += fmt.Sprintf("%s %s - %s\n", status, todo.Title, todo.ID)
		}
		return mcp.NewToolResultText(result), nil
	})

	// Get todo tool
	getTodoTool := mcp.NewTool(
		"get_todo",
		mcp.WithDescription("Get a specific todo by ID"),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("ID of the todo"),
		),
	)

	mcpServer.AddTool(getTodoTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := request.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		todo, err := store.Get(id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		status := "incomplete"
		if todo.Completed {
			status = "complete"
		}
		return mcp.NewToolResultText(fmt.Sprintf("Todo: %s\nDescription: %s\nStatus: %s", todo.Title, todo.Description, status)), nil
	})

	// Complete todo tool
	completeTodoTool := mcp.NewTool(
		"complete_todo",
		mcp.WithDescription("Mark a todo as completed"),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("ID of the todo to complete"),
		),
	)

	mcpServer.AddTool(completeTodoTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := request.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		err = store.Complete(id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Completed todo: %s", id)), nil
	})

	// Delete todo tool
	deleteTodoTool := mcp.NewTool(
		"delete_todo",
		mcp.WithDescription("Delete a todo item"),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("ID of the todo to delete"),
		),
	)

	mcpServer.AddTool(deleteTodoTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := request.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		err = store.Delete(id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Deleted todo: %s", id)), nil
	})
}
