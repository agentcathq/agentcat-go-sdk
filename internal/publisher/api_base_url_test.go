package publisher

import (
	"context"
	"testing"
)

func TestNew_APIBaseURL(t *testing.T) {
	t.Run("uses default URL when apiBaseURL is empty", func(t *testing.T) {
		p := New(nil, "")
		defer p.Shutdown(context.Background())

		if p == nil {
			t.Fatal("New() returned nil")
		}
		if p.apiClient == nil {
			t.Fatal("apiClient not initialized")
		}
		// The API client should have been configured with DefaultAPIBaseURL
		cfg := p.apiClient.GetConfig()
		if len(cfg.Servers) == 0 {
			t.Fatal("no servers configured")
		}
		if cfg.Servers[0].URL != DefaultAPIBaseURL {
			t.Errorf("server URL = %q, want %q", cfg.Servers[0].URL, DefaultAPIBaseURL)
		}
	})

	t.Run("uses custom URL when apiBaseURL is provided", func(t *testing.T) {
		customURL := "https://custom.api.example.com"
		p := New(nil, customURL)
		defer p.Shutdown(context.Background())

		if p == nil {
			t.Fatal("New() returned nil")
		}
		cfg := p.apiClient.GetConfig()
		if len(cfg.Servers) == 0 {
			t.Fatal("no servers configured")
		}
		if cfg.Servers[0].URL != customURL {
			t.Errorf("server URL = %q, want %q", cfg.Servers[0].URL, customURL)
		}
	})
}

func TestGetOrInit_APIBaseURL(t *testing.T) {
	// Ensure clean state
	globalMu.Lock()
	globalPub = nil
	globalMu.Unlock()

	t.Run("passes custom URL through to New", func(t *testing.T) {
		customURL := "https://custom.getinit.example.com"
		p := GetOrInit(nil, customURL)
		defer ShutdownGlobal(context.Background())

		if p == nil {
			t.Fatal("GetOrInit returned nil")
		}
		cfg := p.apiClient.GetConfig()
		if len(cfg.Servers) == 0 {
			t.Fatal("no servers configured")
		}
		if cfg.Servers[0].URL != customURL {
			t.Errorf("server URL = %q, want %q", cfg.Servers[0].URL, customURL)
		}
	})
}
