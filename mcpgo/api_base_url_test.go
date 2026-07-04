package mcpgo

import (
	"testing"
)

func TestOptions_APIBaseURL(t *testing.T) {
	t.Run("default is empty", func(t *testing.T) {
		opts := DefaultOptions()
		if opts.APIBaseURL != "" {
			t.Errorf("default APIBaseURL = %q, want empty string", opts.APIBaseURL)
		}
	})

	t.Run("custom value is preserved", func(t *testing.T) {
		opts := &Options{
			APIBaseURL: "https://custom.api.example.com",
		}
		if opts.APIBaseURL != "https://custom.api.example.com" {
			t.Errorf("APIBaseURL = %q, want %q", opts.APIBaseURL, "https://custom.api.example.com")
		}
	})
}
