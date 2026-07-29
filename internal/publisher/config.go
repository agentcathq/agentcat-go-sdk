package publisher

import "time"

const (
	// QueueSize is the maximum number of events that can be buffered
	QueueSize = 10000

	// MaxWorkers controls the number of parallel API publishing goroutines
	MaxWorkers = 5

	// MaxRetries is the number of times a failed send is retried
	// (in addition to the initial attempt).
	MaxRetries = 3

	// RetryBaseDelay is the backoff delay before the first retry; each
	// subsequent retry doubles it (1s, 2s, 4s).
	RetryBaseDelay = 1 * time.Second

	// RequestTimeout is the per-attempt timeout for publishing an event.
	RequestTimeout = 10 * time.Second

	// DefaultAPIBaseURL is the default AgentCat API base URL
	DefaultAPIBaseURL = "https://api.agentcat.com"
)
