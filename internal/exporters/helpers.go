package exporters

import (
	"io"
	"net/http"
	"time"

	"go.agentcat.com/sdk/internal/core"
)

// orDefault returns s, or def when s is empty.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// eventTimestampMs returns the event timestamp in Unix milliseconds, falling
// back to the current time when unset.
func eventTimestampMs(event *core.Event) int64 {
	if event.Timestamp != nil {
		return event.Timestamp.UnixMilli()
	}
	return time.Now().UnixMilli()
}

// doPost issues an HTTP POST with the given headers using the shared client.
func doPost(url string, headers map[string]string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return httpClient.Do(req)
}
