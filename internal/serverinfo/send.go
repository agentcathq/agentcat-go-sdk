package serverinfo

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"go.agentcat.com/sdk/v2/internal/publisher"
)

// Sender posts one body and returns the HTTP status of the answer, or 0 when
// no answer came.
type Sender func(ctx context.Context, body *Request) (status int, err error)

// sendTimeout bounds one POST.
const sendTimeout = 10 * time.Second

var (
	senderMu sync.RWMutex
	sender   Sender = publisher.SendServerInfo
)

func currentSender() Sender {
	senderMu.RLock()
	defer senderMu.RUnlock()
	return sender
}

// SetSender replaces the sender and returns a function that restores the
// previous one. For tests: the test setup of each module that tracks servers
// installs a recorder, so no test sends a heartbeat to the network.
func SetSender(s Sender) (restore func()) {
	senderMu.Lock()
	prev := sender
	sender = s
	senderMu.Unlock()
	return func() {
		senderMu.Lock()
		sender = prev
		senderMu.Unlock()
	}
}

// sendBody posts the body once. On 413 it posts once more without the tool
// schemas. It never retries otherwise.
func sendBody(ctx context.Context, body *Request) {
	status, err := post(ctx, body)
	if status == http.StatusRequestEntityTooLarge {
		smaller := withoutToolSchemas(body)
		logDebug("server info: body too large, sending again without tool schemas")
		status, err = post(ctx, smaller)
		body = smaller
	}
	if status == http.StatusAccepted {
		tools, _ := body.Features[KeyTools].([]any)
		logDebug("server info: heartbeat sent for %q (%d tools, %d bytes)",
			body.GetServerName(), len(tools), bodySize(body))
		return
	}
	logDebug("server info: heartbeat for %q not accepted (status %d): %v",
		body.GetServerName(), status, err)
}

func post(ctx context.Context, body *Request) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	return currentSender()(ctx, body)
}

func bodySize(body *Request) int {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0
	}
	return len(raw)
}
