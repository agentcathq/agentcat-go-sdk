package officialsdk

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// getOrCreateSession extracts or creates session metadata from the request,
// maintaining a session map keyed by the ServerSession ID.
// It returns a *agentcat.ProtectedSession that callers must lock before accessing fields.
func getOrCreateSession(
	req mcp.Request,
	sessionMap *agentcat.SessionMap,
	serverImpl *mcp.Implementation,
	projectID string,
) *agentcat.ProtectedSession {
	if req == nil {
		return nil
	}

	rawSession := req.GetSession()
	if rawSession == nil {
		return nil
	}

	serverSession, ok := rawSession.(*mcp.ServerSession)
	if !ok || serverSession == nil {
		return nil
	}

	rawSessionID := serverSession.ID()

	// Sessions without a real transport session ID share the "nosessionid"
	// map key and get a random (non-deterministic) session ID.
	isPlaceholder := agentcat.IsPlaceholderSessionID(rawSessionID)
	if isPlaceholder {
		rawSessionID = "nosessionid"
	}

	// Fast path: the session already exists for this raw transport session
	// key, so skip the session ID derivation and allocation.
	ps, loaded := sessionMap.Load(rawSessionID)
	if !loaded {
		// Derive a deterministic session ID from the transport session ID so
		// sessions are stable across server restarts. Fall back to a random
		// ID when there is no real transport session ID (e.g. stdio).
		var formattedSessionID string
		if !isPlaceholder {
			formattedSessionID = agentcat.DeriveSessionID(rawSessionID, projectID)
		} else {
			formattedSessionID = agentcat.NewSessionID()
		}

		newPS := &agentcat.ProtectedSession{
			Sess: &agentcat.Session{
				SessionID: &formattedSessionID,
				ProjectID: &projectID,
			},
		}

		ps, _ = sessionMap.LoadOrStore(rawSessionID, newPS)
	}

	ps.Mu.Lock()
	defer ps.Mu.Unlock()

	if ps.Sess.SdkLanguage == nil {
		ps.Sess.SdkLanguage = agentcat.Ptr("Go")
	}

	if ps.Sess.AgentcatVersion == nil {
		version := agentcat.GetDependencyVersion("go.agentcat.com/sdk")
		ps.Sess.AgentcatVersion = &version
	}

	if ps.Sess.ClientName == nil {
		initParams := serverSession.InitializeParams()
		if initParams != nil && initParams.ClientInfo != nil {
			if initParams.ClientInfo.Name != "" {
				ps.Sess.ClientName = agentcat.Ptr(initParams.ClientInfo.Name)
			}
			if initParams.ClientInfo.Version != "" {
				ps.Sess.ClientVersion = agentcat.Ptr(initParams.ClientInfo.Version)
			}
		}
	}

	if ps.Sess.ServerName == nil && serverImpl != nil {
		if serverImpl.Name != "" {
			ps.Sess.ServerName = agentcat.Ptr(serverImpl.Name)
		}
		if serverImpl.Version != "" {
			ps.Sess.ServerVersion = agentcat.Ptr(serverImpl.Version)
		}
	}

	ps.Touch()
	return ps
}

// updateSessionFromInitResult updates the session with server info from the
// initialize result. The caller must hold ps.Mu.
func updateSessionFromInitResult(ps *agentcat.ProtectedSession, result mcp.Result) {
	if ps == nil || result == nil {
		return
	}
	initResult, ok := result.(*mcp.InitializeResult)
	if !ok || initResult == nil {
		return
	}
	if ps.Sess.ServerName == nil && initResult.ServerInfo != nil {
		if initResult.ServerInfo.Name != "" {
			ps.Sess.ServerName = agentcat.Ptr(initResult.ServerInfo.Name)
		}
		if initResult.ServerInfo.Version != "" {
			ps.Sess.ServerVersion = agentcat.Ptr(initResult.ServerInfo.Version)
		}
	}
}
