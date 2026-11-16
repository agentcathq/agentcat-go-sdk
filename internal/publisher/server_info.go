package publisher

import (
	"context"
	"errors"

	agentcatapi "go.agentcat.com/api"
)

// ErrNoPublisher is returned when no publisher is running, before the first
// Track call or after a shutdown.
var ErrNoPublisher = errors.New("no publisher is running")

// SendServerInfo posts one server info body through the global publisher's
// API client, so it uses the base URL, the headers and the HTTP client of the
// event send. It returns the HTTP status of the answer, or 0 when no answer
// came.
func SendServerInfo(ctx context.Context, body *agentcatapi.PublishServerInfoRequest) (int, error) {
	globalMu.Lock()
	pub := globalPub
	globalMu.Unlock()
	if pub == nil {
		return 0, ErrNoPublisher
	}
	return pub.SendServerInfo(ctx, body)
}

// SendServerInfo posts one server info body with this publisher's API client.
func (p *Publisher) SendServerInfo(ctx context.Context, body *agentcatapi.PublishServerInfoRequest) (int, error) {
	_, resp, err := p.apiClient.ServerInfoAPI.PublishServerInfo(ctx).
		PublishServerInfoRequest(*body).
		Execute()
	if resp == nil {
		return 0, err
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	return resp.StatusCode, err
}
