package mcpgo

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// captureSessionFromContext extracts or creates session metadata from the
// mark3labs/mcp-go context, maintaining a session map keyed by raw session ID.
//
// It returns a *agentcat.ProtectedSession that callers must lock before accessing
// Sess fields.
func captureSessionFromContext(
	ctx context.Context,
	request any,
	response any,
	sessionMap *agentcat.SessionMap,
	opts *Options,
	publishFn func(*agentcat.Event),
) *agentcat.ProtectedSession {
	clientSession := server.ClientSessionFromContext(ctx)
	if clientSession == nil {
		return nil
	}

	rawSessionID := clientSession.SessionID()

	// The tracked project ID is resolved lazily (at most once per call): the
	// registry lookup is only needed on the session-creation slow path and
	// when the stored session doesn't have a project ID yet.
	var projectID string
	projectIDResolved := false
	resolveProjectID := func() string {
		if !projectIDResolved {
			projectID = lookupProjectID(ctx)
			projectIDResolved = true
		}
		return projectID
	}

	// Fast path: the session already exists for this raw transport session
	// key, so skip the registry lookup, session ID derivation, and allocation.
	ps, loaded := sessionMap.Load(rawSessionID)
	if !loaded {
		// Derive a deterministic session ID from the transport session ID so
		// sessions are stable across server restarts. Fall back to a random ID
		// when there is no real transport session ID (stdio reports a
		// constant). The project ID is included in the derivation.
		var formattedSessionID string
		if !agentcat.IsPlaceholderSessionID(rawSessionID) {
			formattedSessionID = agentcat.DeriveSessionID(rawSessionID, resolveProjectID())
		} else {
			formattedSessionID = agentcat.NewSessionID()
		}

		newPS := &agentcat.ProtectedSession{
			Sess: &agentcat.Session{
				SessionID: &formattedSessionID,
			},
		}

		ps, _ = sessionMap.LoadOrStore(rawSessionID, newPS)
	}

	// Update session fields under lock; the lock is released via defer so a
	// panic can never leave the session mutex held.
	func() {
		ps.Mu.Lock()
		defer ps.Mu.Unlock()

		if ps.Sess.ProjectID == nil {
			if pid := resolveProjectID(); pid != "" {
				ps.Sess.ProjectID = &pid
			}
		}

		if ps.Sess.SdkLanguage == nil {
			ps.Sess.SdkLanguage = agentcat.Ptr("Go")
		}

		if ps.Sess.AgentcatVersion == nil {
			version := agentcat.GetDependencyVersion("go.agentcat.com/sdk")
			ps.Sess.AgentcatVersion = &version
		}

		if sessionWithInfo, ok := clientSession.(server.SessionWithClientInfo); ok {
			clientInfo := sessionWithInfo.GetClientInfo()

			if clientInfo.Name != "" && ps.Sess.ClientName == nil {
				ps.Sess.ClientName = agentcat.Ptr(clientInfo.Name)
			}
			if clientInfo.Version != "" && ps.Sess.ClientVersion == nil {
				ps.Sess.ClientVersion = agentcat.Ptr(clientInfo.Version)
			}
		}

		if initializeResult, ok := response.(*mcp.InitializeResult); ok {
			serverInfo := initializeResult.ServerInfo
			if ps.Sess.ServerName == nil {
				ps.Sess.ServerName = agentcat.Ptr(serverInfo.Name)
			}
			if ps.Sess.ServerVersion == nil {
				ps.Sess.ServerVersion = agentcat.Ptr(serverInfo.Version)
			}
		}

	}()

	// Identify runs on every captured event, outside the session lock (the
	// user callback may be slow or block). mcp-go's hook dispatch fires
	// exactly one of OnSuccess/OnError per request, so identify runs once per
	// captured request.
	if opts != nil && opts.Identify != nil {
		handleIdentify(ctx, opts, request, ps, publishFn)
	}

	ps.Touch()
	return ps
}

// lookupProjectID resolves the tracked project ID for the server in the
// context, or "" when the server is not registered.
func lookupProjectID(ctx context.Context) string {
	mcpServer := server.ServerFromContext(ctx)
	if mcpServer == nil {
		return ""
	}
	if tracker := agentcat.GetInstance(mcpServer); tracker != nil {
		return tracker.ProjectID
	}
	return ""
}

// handleIdentify runs the Identify callback for a captured request and, when
// it returns a non-nil identity, merges it into the session identity and
// publishes an agentcat:identify event. UserID and UserName are overwritten;
// UserData is merged. A panic in the callback is recovered and logged.
func handleIdentify(
	ctx context.Context,
	opts *Options,
	request any,
	ps *agentcat.ProtectedSession,
	publishFn func(*agentcat.Event),
) {
	identifyInfo := safeIdentify(ctx, opts, request)
	if identifyInfo == nil {
		return
	}

	// Merge and stamp under the session lock (owned by ApplyIdentity);
	// publish outside the lock.
	_, identifyEvent := ps.ApplyIdentity(identifyInfo)

	if identifyEvent != nil {
		publishFn(identifyEvent)
	}
}

// safeIdentify invokes the user-supplied Identify callback with panic
// recovery so a faulty callback can never break the customer's server.
func safeIdentify(ctx context.Context, opts *Options, request any) (identity *agentcat.UserIdentity) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("mcpgo Identify callback", r)
			identity = nil
		}
	}()
	return opts.Identify(ctx, request)
}
