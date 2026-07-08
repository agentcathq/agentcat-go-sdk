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
	Exporter         = core.Exporter
	ExporterConfig   = core.ExporterConfig
	Event            = core.Event
	AgentCatInstance = core.AgentCatInstance
	Session          = core.Session
	CustomEventData  = core.CustomEventData
	ProtectedSession = sessionmap.ProtectedSession
	SessionMap       = sessionmap.SessionMap
)

// MCPcatInstance is the former name of AgentCatInstance.
//
// Deprecated: use AgentCatInstance.
type MCPcatInstance = AgentCatInstance

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
