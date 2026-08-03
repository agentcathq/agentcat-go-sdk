module go.agentcat.com/sdk/examples/mcpgo/basic

go 1.25.5

require (
	github.com/mark3labs/mcp-go v0.57.0
	go.agentcat.com/sdk/mcpgo/v2 v2.0.0-beta.1
)

require (
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/segmentio/ksuid v1.0.4 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.agentcat.com/api v1.0.0 // indirect
	go.agentcat.com/sdk/v2 v2.0.0-beta.1 // indirect
	golang.org/x/text v0.14.0 // indirect
	gopkg.in/validator.v2 v2.0.1 // indirect
)

replace (
	go.agentcat.com/sdk/mcpgo/v2 => ../../../mcpgo
	go.agentcat.com/sdk/v2 => ../../../
)
