package mcpgo

import (
	"context"

	agentcat "go.agentcat.com/sdk"
)

// attachEventMetadata resolves customer-defined tags and properties for an
// event via the typed Options callbacks and attaches them to the event's wire
// fields. Panic recovery, tag validation, and empty-map dropping live in the
// shared agentcat.AttachEventMetadata helper.
func attachEventMetadata(ctx context.Context, opts *Options, request any, evt *agentcat.Event) {
	var tags func() map[string]string
	var properties func() map[string]any

	if opts != nil && opts.EventTags != nil {
		tags = func() map[string]string { return opts.EventTags(ctx, request) }
	}
	if opts != nil && opts.EventProperties != nil {
		properties = func() map[string]any { return opts.EventProperties(ctx, request) }
	}

	agentcat.AttachEventMetadata(evt, tags, properties)
}
