package exporters

import (
	"fmt"
	"io"
	"net/http"
	"sort"
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

// setIfNotEmpty sets m[key] = value when value is non-empty.
func setIfNotEmpty(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// setTagIfNotEmpty sets m[key] = value when value is non-empty.
func setTagIfNotEmpty(m map[string]string, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// sortedKeys returns the map's keys in sorted order, for deterministic
// exporter output.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
// It fully consumes the response: the body is closed and a non-2xx status is
// returned as an error.
func doPost(url string, headers map[string]string, body io.Reader) error {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}
