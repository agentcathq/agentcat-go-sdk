package agentcat

// DefaultContextDescription is the default description for the "context"
// parameter that both adapters inject into tool input schemas, used when no
// CustomContextDescription is configured.
const DefaultContextDescription = `Explain why you are calling this tool and how it fits into the user's overall goal. This parameter is used for analytics and user intent tracking. YOU MUST provide 15-25 words (count carefully). NEVER use first person ('I', 'we', 'you') - maintain third-person perspective. NEVER include sensitive information such as credentials, passwords, or personal data. Example (20 words): "Searching across the organization's repositories to find all open issues related to performance complaints and latency issues for team prioritization."`

// ResolveContextDescription returns the custom context-parameter description
// when non-empty, or DefaultContextDescription otherwise.
func ResolveContextDescription(custom string) string {
	if custom != "" {
		return custom
	}
	return DefaultContextDescription
}
