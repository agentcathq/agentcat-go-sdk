package publisher

const (
	// QueueSize is the maximum number of events that can be buffered
	QueueSize = 1000

	// MaxWorkers controls the number of parallel API publishing goroutines
	MaxWorkers = 5

	// DefaultAPIBaseURL is the default MCPCat API base URL
	DefaultAPIBaseURL = "https://api.mcpcat.io"
)
