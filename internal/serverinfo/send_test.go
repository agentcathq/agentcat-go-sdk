package serverinfo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.agentcat.com/sdk/v2/internal/publisher"
)

// recorder answers each POST with the next status of its script and keeps
// every body it got.
type recorder struct {
	mu       sync.Mutex
	statuses []int
	bodies   []*Request
}

func (r *recorder) send(_ context.Context, body *Request) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, body)
	status := http.StatusAccepted
	if len(r.statuses) > 0 {
		status, r.statuses = r.statuses[0], r.statuses[1:]
	}
	if status != http.StatusAccepted {
		return status, errors.New(http.StatusText(status))
	}
	return status, nil
}

func (r *recorder) got() []*Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Request(nil), r.bodies...)
}

func useRecorder(t *testing.T, statuses ...int) *recorder {
	t.Helper()
	rec := &recorder{statuses: statuses}
	t.Cleanup(SetSender(rec.send))
	return rec
}

func schemaBody() *Request {
	body := &Request{ProjectId: demoTarget.ProjectID}
	body.SetServerName("demo-server")
	body.Features = map[string]any{
		KeyTools: []any{map[string]any{
			"name":         "beta",
			"title":        "Beta",
			"description":  "the beta tool",
			"inputSchema":  map[string]any{"type": "object"},
			"outputSchema": map[string]any{"type": "object"},
		}},
		KeyPrompts: []any{map[string]any{"name": "p", "inputSchema": "kept: not a tool"}},
	}
	return body
}

func TestSend_V13_413SendsOnceMoreWithoutSchemas(t *testing.T) {
	rec := useRecorder(t, http.StatusRequestEntityTooLarge, http.StatusAccepted)
	sendBody(context.Background(), schemaBody())

	bodies := rec.got()
	if len(bodies) != 2 {
		t.Fatalf("POSTs = %d, want 2", len(bodies))
	}
	first := bodies[0].Features[KeyTools].([]any)[0].(map[string]any)
	if _, ok := first["inputSchema"]; !ok {
		t.Fatal("first POST has no inputSchema")
	}
	second := bodies[1].Features[KeyTools].([]any)[0].(map[string]any)
	for _, key := range []string{"inputSchema", "outputSchema"} {
		if _, ok := second[key]; ok {
			t.Errorf("second POST still has %s", key)
		}
	}
	for _, key := range []string{"name", "title", "description"} {
		if second[key] != first[key] {
			t.Errorf("%s = %v, want %v", key, second[key], first[key])
		}
	}
	prompt := bodies[1].Features[KeyPrompts].([]any)[0].(map[string]any)
	if _, ok := prompt["inputSchema"]; !ok {
		t.Error("a prompt lost a field: only tools drop their schemas")
	}
}

func TestSend_V13_Second413EndsTheAttempt(t *testing.T) {
	rec := useRecorder(t, http.StatusRequestEntityTooLarge, http.StatusRequestEntityTooLarge, http.StatusAccepted)
	sendBody(context.Background(), schemaBody())
	if got := len(rec.got()); got != 2 {
		t.Fatalf("POSTs = %d, want 2", got)
	}
}

func TestSend_V10_500IsNotSentAgain(t *testing.T) {
	rec := useRecorder(t, http.StatusInternalServerError, http.StatusAccepted)
	sendBody(context.Background(), schemaBody())
	if got := len(rec.got()); got != 1 {
		t.Fatalf("POSTs = %d, want 1", got)
	}
}

func TestSend_V14_PostsToTheBaseURLOfTheEventSend(t *testing.T) {
	type seen struct {
		path, auth, contentType string
		body                    map[string]any
	}
	got := make(chan seen, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		got <- seen{path: r.URL.Path, auth: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type"), body: body}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	pub := publisher.New(nil, nil, srv.URL, nil)
	defer pub.Shutdown(context.Background())
	t.Cleanup(SetSender(pub.SendServerInfo))

	sendBody(context.Background(), schemaBody())

	select {
	case s := <-got:
		if s.path != "/v1/public/server-info/" {
			t.Errorf("path = %q, want /v1/public/server-info/", s.path)
		}
		if s.auth != "" {
			t.Errorf("Authorization = %q, want none", s.auth)
		}
		if s.contentType != "application/json" {
			t.Errorf("Content-Type = %q", s.contentType)
		}
		if s.body["server_name"] != "demo-server" || s.body["project_id"] != demoTarget.ProjectID {
			t.Errorf("body = %v", s.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no POST reached the custom base URL")
	}
	select {
	case <-got:
		t.Fatal("a second POST followed a 202")
	default:
	}
}

func TestSend_RealSenderReports413(t *testing.T) {
	var posts sync.WaitGroup
	posts.Add(2)
	var mu sync.Mutex
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		n := count
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":"too large"}`))
		} else {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		}
		posts.Done()
	}))
	defer srv.Close()

	pub := publisher.New(nil, nil, srv.URL, nil)
	defer pub.Shutdown(context.Background())
	t.Cleanup(SetSender(pub.SendServerInfo))

	sendBody(context.Background(), schemaBody())
	posts.Wait()
	mu.Lock()
	defer mu.Unlock()
	if count != 2 {
		t.Fatalf("POSTs = %d, want 2", count)
	}
}

func TestSend_NoPublisherSendsNothing(t *testing.T) {
	_ = publisher.ShutdownGlobal(context.Background())
	status, err := publisher.SendServerInfo(context.Background(), schemaBody())
	if status != 0 || !errors.Is(err, publisher.ErrNoPublisher) {
		t.Fatalf("status %d, err %v; want 0 and ErrNoPublisher", status, err)
	}
}
