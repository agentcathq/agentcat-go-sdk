package agentcat

import "go.agentcat.com/sdk/v2/internal/tokens"

// ApplyTokenEstimates fills InputTokens from Parameters["arguments"] and
// OutputTokens from Response, each only when that payload is present. The
// adapters call it once both are set and before publish, so the estimates
// describe the raw payloads — the publisher's redaction hooks run later and
// never recompute them. See internal/tokens.
func ApplyTokenEstimates(evt *Event) {
	if evt == nil {
		return
	}
	// Belt and braces under the adapters' own recover: a failure to estimate
	// costs at most these two fields, never the customer's response.
	defer func() {
		if r := recover(); r != nil {
			LogRecoveredPanic("token estimates", r)
		}
	}()
	if args, ok := evt.Parameters["arguments"].(map[string]any); ok {
		if n, ok := tokens.InputTokens(args); ok {
			evt.InputTokens = &n
		}
	}
	if n, ok := tokens.OutputTokens(evt.Response); ok {
		evt.OutputTokens = &n
	}
}
