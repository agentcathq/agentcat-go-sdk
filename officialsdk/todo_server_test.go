package officialsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// Todo represents a single todo item.
type Todo struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

// TodoStore is a thread-safe in-memory store for todo items.
type TodoStore struct {
	mu     sync.Mutex
	todos  map[int]*Todo
	nextID int
}

func newTodoStore() *TodoStore {
	return &TodoStore{
		todos:  make(map[int]*Todo),
		nextID: 1,
	}
}

func (s *TodoStore) add(title string) *Todo {
	s.mu.Lock()
	defer s.mu.Unlock()
	todo := &Todo{
		ID:        s.nextID,
		Title:     title,
		Completed: false,
	}
	s.todos[s.nextID] = todo
	s.nextID++
	return todo
}

func (s *TodoStore) get(id int) (*Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	todo, ok := s.todos[id]
	if !ok {
		return nil, fmt.Errorf("todo with id %d not found", id)
	}
	return todo, nil
}

func (s *TodoStore) list() []*Todo {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*Todo, 0, len(s.todos))
	for _, todo := range s.todos {
		result = append(result, todo)
	}
	return result
}

func (s *TodoStore) complete(id int) (*Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	todo, ok := s.todos[id]
	if !ok {
		return nil, fmt.Errorf("todo with id %d not found", id)
	}
	todo.Completed = true
	return todo, nil
}

func (s *TodoStore) delete(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.todos[id]; !ok {
		return fmt.Errorf("todo with id %d not found", id)
	}
	delete(s.todos, id)
	return nil
}

// Arg structs for typed mcp.AddTool registration.
type addTodoArgs struct {
	Title string `json:"title" jsonschema:"Title of the todo item"`
}
type listTodosArgs struct{}
type getTodoArgs struct {
	ID float64 `json:"id" jsonschema:"ID of the todo item"`
}
type completeTodoArgs struct {
	ID float64 `json:"id" jsonschema:"ID of the todo item to complete"`
}
type deleteTodoArgs struct {
	ID float64 `json:"id" jsonschema:"ID of the todo item to delete"`
}
type todoTextResult struct {
	Text string `json:"text"`
}

// createFullTestServer creates a server with tools, resources, and prompts for
// end-to-end testing.
func createFullTestServer(t *testing.T) (*mcp.Server, *TodoStore) {
	t.Helper()

	store := newTodoStore()

	serverImpl := &mcp.Implementation{Name: "todo-test-server", Version: "1.0.0"}
	server := mcp.NewServer(serverImpl, nil)

	// Tool: add_todo
	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_todo",
		Description: "Add a new todo item",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args addTodoArgs) (*mcp.CallToolResult, todoTextResult, error) {
		todo := store.add(args.Title)
		data, _ := json.Marshal(todo)
		return nil, todoTextResult{Text: string(data)}, nil
	})

	// Tool: list_todos
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_todos",
		Description: "List all todo items",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args listTodosArgs) (*mcp.CallToolResult, todoTextResult, error) {
		todos := store.list()
		data, _ := json.Marshal(todos)
		return nil, todoTextResult{Text: string(data)}, nil
	})

	// Tool: get_todo
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_todo",
		Description: "Get a todo item by ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args getTodoArgs) (*mcp.CallToolResult, todoTextResult, error) {
		todo, err := store.get(int(args.ID))
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, todoTextResult{}, nil
		}
		data, _ := json.Marshal(todo)
		return nil, todoTextResult{Text: string(data)}, nil
	})

	// Tool: complete_todo
	mcp.AddTool(server, &mcp.Tool{
		Name:        "complete_todo",
		Description: "Mark a todo item as completed",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args completeTodoArgs) (*mcp.CallToolResult, todoTextResult, error) {
		todo, err := store.complete(int(args.ID))
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, todoTextResult{}, nil
		}
		data, _ := json.Marshal(todo)
		return nil, todoTextResult{Text: string(data)}, nil
	})

	// Tool: delete_todo
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_todo",
		Description: "Delete a todo item by ID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args deleteTodoArgs) (*mcp.CallToolResult, todoTextResult, error) {
		if err := store.delete(int(args.ID)); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, todoTextResult{}, nil
		}
		return nil, todoTextResult{Text: "todo deleted successfully"}, nil
	})

	// Resource: todo://list
	server.AddResource(&mcp.Resource{
		URI:         "todo://list",
		Name:        "todo-list",
		Description: "List of all todo items",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		todos := store.list()
		data, _ := json.Marshal(todos)
		text := string(data)
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:  "todo://list",
					Text: text,
				},
			},
		}, nil
	})

	// Prompt: summarize_todos
	server.AddPrompt(&mcp.Prompt{
		Name:        "summarize_todos",
		Description: "Summarize the current todo list",
		Arguments: []*mcp.PromptArgument{
			{
				Name:        "style",
				Description: "The summary style (brief, detailed, or bullet)",
				Required:    true,
			},
		},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		style := req.Params.Arguments["style"]
		todos := store.list()
		data, _ := json.Marshal(todos)

		promptText := fmt.Sprintf("Please summarize the following todos in a %s style:\n%s", style, string(data))

		return &mcp.GetPromptResult{
			Description: "Todo list summary prompt",
			Messages: []*mcp.PromptMessage{
				{
					Role:    mcp.Role("user"),
					Content: &mcp.TextContent{Text: promptText},
				},
			},
		}, nil
	})

	return server, store
}

// createFullTestServerWithTracking wraps createFullTestServer and installs
// MCPCat tracking middleware with a mock publisher for test assertions.
func createFullTestServerWithTracking(t *testing.T, opts *Options) (*mcp.Server, *TodoStore, *mockPublisher) {
	t.Helper()

	server, store := createFullTestServer(t)

	mock := &mockPublisher{}

	if opts == nil {
		opts = DefaultOptions()
	}

	projectID := "proj_test"
	serverImpl := &mcp.Implementation{Name: "todo-test-server", Version: "1.0.0"}

	coreOpts := &agentcat.Options{
		DisableReportMissing:       opts.DisableReportMissing,
		DisableToolCallContext:     opts.DisableToolCallContext,
		DisableTracing:             opts.DisableTracing,
		CustomContextDescription:   opts.CustomContextDescription,
		Debug:                      opts.Debug,
		RedactSensitiveInformation: opts.RedactSensitiveInformation,
	}

	instance := &agentcat.AgentCatInstance{
		ProjectID: projectID,
		Options:   coreOpts,
		ServerRef: server,
		SessionID: agentcat.NewSessionID(),
	}
	agentcat.RegisterServer(server, instance)

	middleware, sessionMap := newTrackingMiddleware(projectID, opts, mock.publish, serverImpl)
	defer sessionMap.Stop()
	server.AddReceivingMiddleware(middleware)

	registerGetMoreToolsIfEnabled(server, coreOpts)

	t.Cleanup(func() {
		agentcat.UnregisterServer(server)
	})

	return server, store, mock
}
