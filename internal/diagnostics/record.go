package diagnostics

import (
	"strconv"
	"time"

	"go.agentcat.com/sdk/internal/logging"
)

// severityFor maps a log level to an OTLP severity number and text.
// LevelDebug is filtered before this is called.
func severityFor(level logging.Level) (int, string) {
	switch level {
	case logging.LevelError:
		return 17, "ERROR"
	case logging.LevelWarn:
		return 13, "WARN"
	default:
		return 9, "INFO"
	}
}

func buildRecord(level logging.Level, msg string) otlpLogRecord {
	num, text := severityFor(level)
	return otlpLogRecord{
		TimeUnixNano:   strconv.FormatInt(time.Now().UnixNano(), 10),
		SeverityNumber: num,
		SeverityText:   text,
		Body:           otlpBody{StringValue: msg},
		Attributes:     []otlpAttribute{},
	}
}

// BuildRecordForTest exposes buildRecord for tests.
func BuildRecordForTest(level logging.Level, msg string) otlpLogRecord {
	return buildRecord(level, msg)
}
