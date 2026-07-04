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

	formattedSessionID := agentcat.NewSessionID()
	newPS := &agentcat.ProtectedSession{
		Sess: &agentcat.Session{
			SessionID: &formattedSessionID,
		},
	}

	ps, _ := sessionMap.LoadOrStore(rawSessionID, newPS)

	ps.Mu.Lock()

	if ps.Sess.ProjectID == nil {
		mcpServer := server.ServerFromContext(ctx)
		if mcpServer != nil {
			if tracker := agentcat.GetInstance(mcpServer); tracker != nil {
				ps.Sess.ProjectID = &tracker.ProjectID
			}
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

	var shouldIdentify bool
	if opts != nil && opts.Identify != nil {
		if _, ok := request.(*mcp.CallToolRequest); ok {
			shouldIdentify = true
		}
	}

	ps.Mu.Unlock()

	if shouldIdentify {
		ps.IdentifyOnce.Do(func() {
			toolReq := request.(*mcp.CallToolRequest)
			identifyInfo := opts.Identify(ctx, toolReq)
			if identifyInfo == nil {
				return
			}

			ps.Mu.Lock()
			ps.Sess.IdentifyActorGivenId = &identifyInfo.UserID
			ps.Sess.IdentifyActorName = &identifyInfo.UserName
			ps.Sess.IdentifyData = identifyInfo.UserData
			identifyEvent := agentcat.CreateIdentifyEvent(ps.Sess)
			ps.Mu.Unlock()
			if identifyEvent != nil {
				publishFn(identifyEvent)
			}
		})
	}

	ps.Touch()
	return ps
}
