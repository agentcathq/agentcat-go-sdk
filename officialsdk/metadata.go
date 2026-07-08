package officialsdk

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// resolveEventTags invokes the EventTags callback (if configured) and
// validates the result. A panic in the callback is swallowed so the event is
// still published without tags.
func resolveEventTags(ctx context.Context, opts *Options, request mcp.Request) (tags map[string]string) {
	if opts == nil || opts.EventTags == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			tags = nil
		}
	}()
	return agentcat.ValidateTags(opts.EventTags(ctx, request))
}

// resolveEventProperties invokes the EventProperties callback (if configured).
// A panic in the callback is swallowed so the event is still published
// without properties.
func resolveEventProperties(ctx context.Context, opts *Options, request mcp.Request) (properties map[string]any) {
	if opts == nil || opts.EventProperties == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			properties = nil
		}
	}()
	props := opts.EventProperties(ctx, request)
	if len(props) == 0 {
		return nil
	}
	return props
}

// attachEventMetadata resolves tags and properties for an event and attaches
// them to the event's wire fields.
func attachEventMetadata(ctx context.Context, opts *Options, request mcp.Request, evt *agentcat.Event) {
	if evt == nil {
		return
	}
	if tags := resolveEventTags(ctx, opts, request); tags != nil {
		evt.Tags = &tags
	}
	if properties := resolveEventProperties(ctx, opts, request); properties != nil {
		evt.Properties = properties
	}
}
