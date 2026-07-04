package diagnostics

type otlpAttrValue struct {
	StringValue string `json:"stringValue"`
}

type otlpAttribute struct {
	Key   string        `json:"key"`
	Value otlpAttrValue `json:"value"`
}

type otlpBody struct {
	StringValue string `json:"stringValue"`
}

type otlpLogRecord struct {
	TimeUnixNano   string          `json:"timeUnixNano"`
	SeverityNumber int             `json:"severityNumber"`
	SeverityText   string          `json:"severityText"`
	Body           otlpBody        `json:"body"`
	Attributes     []otlpAttribute `json:"attributes"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type otlpScopeLogs struct {
	Scope      otlpScope       `json:"scope"`
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpResourceLogs struct {
	Resource  otlpResource    `json:"resource"`
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpPayload struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}
