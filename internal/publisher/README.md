# Event Publisher

The `publisher` package provides asynchronous event publishing to the AgentCat API.

## Overview

Events are queued in an in-memory channel and published to the API by a pool of worker goroutines. This ensures that event publishing does not block the main request/response flow of the MCP server.

## Architecture

- **Queue**: Buffered channel with capacity of 1000 events
- **Workers**: Pool of 5 goroutines that process events in parallel
- **Backpressure**: When queue is full, newest events are dropped (not oldest)
- **Graceful Shutdown**: Waits up to 5 seconds for queued events to be published

## Configuration

The publisher uses the default AgentCat API at `https://api.mcpcat.io`. No configuration or API key is required - the AgentCat API is public.

## Usage

The publisher is automatically initialized when you call `mcpcat.Track()`. Events are published asynchronously whenever tool calls complete.

### Automatic Shutdown

The publisher **automatically handles graceful shutdown** when your application receives `SIGINT` (Ctrl+C) or `SIGTERM` signals:

```go
func main() {
    // Setup tracking
    mcpcat.Track(s, "proj_YOUR_PROJECT_ID", nil)

    // Start server - automatic shutdown on Ctrl+C!
    server.ServeStdio(s)
}
```

When you press Ctrl+C, the publisher will:
1. Stop accepting new events
2. Wait up to 5 seconds for queued events to publish
3. Exit gracefully with all events published

### Manual Shutdown (Recommended)

While automatic shutdown handles most cases, adding `defer mcpcat.Shutdown()` is **recommended** as a best practice to ensure graceful shutdown in all scenarios:

```go
func main() {
    defer mcpcat.Shutdown() // Recommended: handles os.Exit(), panic, etc.

    mcpcat.Track(s, "proj_YOUR_PROJECT_ID", nil)
    server.ServeStdio(s)
}
```

This ensures graceful shutdown even when:
- Your code calls `os.Exit()` directly
- The program panics (without recovery)
- Normal function return

### Multiple Server Instances

The publisher is a **global singleton** - all MCP servers in your process share the same event queue and worker pool:

```go
func main() {
    defer mcpcat.Shutdown() // Single shutdown call for all servers

    // Server 1
    mcpcat.Track(server1, "proj_ID_1", nil)

    // Server 2 - shares the same publisher!
    mcpcat.Track(server2, "proj_ID_2", nil)

    // Both publish to the same queue
    http.ListenAndServe(":8080", nil)
}
```

## Performance Characteristics

- **Non-blocking**: Event publishing never blocks the request/response cycle
- **Parallel**: Up to 5 concurrent API requests (configurable via `MaxWorkers`)
- **Memory**: Maximum ~1000 events buffered in memory at once
- **Timeout**: API requests timeout after 10 seconds
- **Shutdown**: Waits up to 5 seconds for remaining events to publish

## Error Handling

All errors during event publishing are logged to `~/mcpcat.log` but do not cause the application to crash or affect the MCP server operation.
