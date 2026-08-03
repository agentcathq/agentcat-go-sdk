// Example: AgentCat inside a per-request server factory (official Go MCP SDK)
//
// MCP 2026-07-28 removed protocol-level sessions, so the expected 2026
// deployment is a stateless HTTP endpoint behind a load balancer: every request
// builds a fresh mcp.Server, serves one request, and throws it away. Nothing is
// carried between requests, and consecutive calls from the same agent routinely
// land on different processes.
//
// AgentCat is built for exactly this topology — call Track() inside the
// factory. Correlation rides on the explicit session_id handle the agent echoes,
// not on any server-side state.
//
// Usage:
//
//	go run . (runs as a stateless MCP server over HTTP Streamable on port 8080)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/officialsdk/v2"
)

type EchoArgs struct {
	Text string `json:"text" jsonschema:"the text to echo"`
}

type CountCharsArgs struct {
	Text string `json:"text" jsonschema:"the text to count characters in"`
}

type TextResult struct {
	Text string `json:"text"`
}

// newServer builds a complete MCP server from scratch. In a stateless
// deployment this runs once per HTTP request, so keep it cheap: no network
// calls and no shared mutable state.
func newServer() *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{
			Name:    "officialsdk-factory-example",
			Version: "1.0.0",
		},
		nil,
	)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "echo",
		Description: "Echo back the input text",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args EchoArgs) (*mcp.CallToolResult, TextResult, error) {
		return nil, TextResult{Text: args.Text}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "count_chars",
		Description: "Count the number of characters in the input text",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args CountCharsArgs) (*mcp.CallToolResult, TextResult, error) {
		return nil, TextResult{Text: fmt.Sprintf("%d", len([]rune(args.Text)))}, nil
	})

	return s
}

func main() {
	projectID := os.Getenv("MCPCAT_PROJECT_ID")
	if projectID == "" {
		projectID = "proj_YOUR_PROJECT_ID"
	}

	// --- AgentCat: Track() inside the factory ---
	//
	// getServer runs on every HTTP request. Tracking the freshly built server
	// right here is the supported pattern:
	//
	//   - Module-level state (the event publisher, the debug logger, the
	//     diagnostics channel) initializes exactly once, however many servers
	//     you track.
	//   - Per-server state lives in AgentCat's registry, keyed by the server
	//     pointer, and is released automatically once the server becomes
	//     unreachable — so a per-request factory does not leak an entry per
	//     request. Nothing AgentCat stores points back at your server.
	//   - Handles are stateless: the session_id the agent echoes carries the
	//     correlation, so a process that has never seen this agent before still
	//     attributes the call to the right session.
	//   - Rebuild on demand: a stateless client can send tools/call to this
	//     instance without a tools/list ever reaching it. When that happens
	//     AgentCat rebuilds its injection registries from the server's own tool
	//     list on the first call, so injected arguments are still stripped
	//     before your handler sees them and the minted session_id still reaches
	//     the agent.
	//
	// Track returns a per-server shutdown function. Ignore it here — the
	// publisher is process-wide, so drain it once at exit instead (below).
	getServer := func(r *http.Request) *mcp.Server {
		s := newServer()
		if _, err := agentcat.Track(s, projectID, nil); err != nil {
			// Analytics must never fail a customer request: log and serve.
			log.Printf("agentcat: %v", err)
		}
		return s
	}
	// --- end AgentCat ---

	handler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})

	httpServer := &http.Server{Addr: ":8080", Handler: handler}

	// Serve until SIGINT/SIGTERM, then flush queued events once for the whole
	// process — there is no per-request shutdown to call.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("Stateless MCP server listening on http://localhost:8080/mcp")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down")

	// Drain connections first so in-flight calls get their events queued...
	httpCtx, cancelHTTP := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHTTP()
	if err := httpServer.Shutdown(httpCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// ...then flush them on a BUDGET OF ITS OWN. Sharing one deadline with the
	// connection drain means a slow client eats the flush window and queued
	// events are silently dropped.
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFlush()
	if err := agentcat.Shutdown(flushCtx); err != nil {
		log.Printf("agentcat shutdown: %v", err)
	}
}
