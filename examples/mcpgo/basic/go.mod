module go.agentcat.com/sdk/examples/mcpgo/basic

go 1.24.0

toolchain go1.24.4

require (
	github.com/mark3labs/mcp-go v0.44.1
	go.agentcat.com/sdk/mcpgo v0.3.0
)

require (
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/invopop/jsonschema v0.13.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/segmentio/ksuid v1.0.4 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.agentcat.com/api v0.0.0-20260704194121-ad7827fb5ba1 // indirect
	go.agentcat.com/sdk v0.3.0 // indirect
	gopkg.in/validator.v2 v2.0.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	go.agentcat.com/sdk => ../../../
	go.agentcat.com/sdk/mcpgo => ../../../mcpgo
)
