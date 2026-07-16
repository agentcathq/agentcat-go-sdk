package agentcat

import (
	"time"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/sessionmap"
)

type (
	UserIdentity     = core.UserIdentity
	Options          = core.Options
	RedactFunc       = core.RedactFunc
	RedactEventFunc  = core.RedactEventFunc
	Exporter         = core.Exporter
	ExporterConfig   = core.ExporterConfig
	Event            = core.Event
	MCPcatInstance   = core.MCPcatInstance
	Session          = core.Session
	ProtectedSession = sessionmap.ProtectedSession
	SessionMap       = sessionmap.SessionMap
)

type IDPrefix = core.IDPrefix

const (
	PrefixSession IDPrefix = core.PrefixSession
	PrefixEvent   IDPrefix = core.PrefixEvent
)

func DefaultOptions() Options {
	return core.DefaultOptions()
}

// NewSessionMap creates a session map with TTL-based eviction.
// If ttl is 0, a 30-minute default is used.
func NewSessionMap(ttl time.Duration) *SessionMap {
	return sessionmap.New(ttl)
}
