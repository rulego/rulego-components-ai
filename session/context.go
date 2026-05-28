package session

import (
	"context"

	"github.com/rulego/rulego-components-ai/utils/contextx"
)

// Type-safe context keys using generic Key
var (
	// sessionKey stores *Session
	sessionKey = contextx.NewKey[*Session]("session")
	// sessionKeyStr stores session key string
	sessionKeyStr = contextx.NewKey[string]("sessionKey")
	// sessionManagerKey stores SessionManager
	sessionManagerKey = contextx.NewKey[SessionManager]("sessionManager")
	// agentIDKey stores agent ID string
	agentIDKey = contextx.NewKey[string]("agentId")
	// channelKey stores channel string
	channelKey = contextx.NewKey[string]("channel")
	// userIDKey stores user ID string
	userIDKey = contextx.NewKey[string]("userId")
)

// SessionFromContext retrieves session from context
func SessionFromContext(ctx context.Context) (*Session, bool) {
	return sessionKey.Get(ctx)
}

// SessionKeyFromContext retrieves session key from context
func SessionKeyFromContext(ctx context.Context) (string, bool) {
	return sessionKeyStr.Get(ctx)
}

// SessionManagerFromContext retrieves session manager from context
func SessionManagerFromContext(ctx context.Context) (SessionManager, bool) {
	return sessionManagerKey.Get(ctx)
}

// AgentIDFromContext retrieves agent ID from context
func AgentIDFromContext(ctx context.Context) (string, bool) {
	return agentIDKey.Get(ctx)
}

// ChannelFromContext retrieves channel from context
func ChannelFromContext(ctx context.Context) (string, bool) {
	return channelKey.Get(ctx)
}

// UserIDFromContext retrieves user ID from context
func UserIDFromContext(ctx context.Context) (string, bool) {
	return userIDKey.Get(ctx)
}

// WithSession injects session into context
func WithSession(ctx context.Context, session *Session) context.Context {
	return sessionKey.With(ctx, session)
}

// WithSessionKey injects session key into context
func WithSessionKey(ctx context.Context, key string) context.Context {
	return sessionKeyStr.With(ctx, key)
}

// WithSessionManager injects session manager into context
func WithSessionManager(ctx context.Context, sm SessionManager) context.Context {
	return sessionManagerKey.With(ctx, sm)
}

// WithAgentID injects agent ID into context
func WithAgentID(ctx context.Context, agentId string) context.Context {
	return agentIDKey.With(ctx, agentId)
}

// WithChannel injects channel into context
func WithChannel(ctx context.Context, channel string) context.Context {
	return channelKey.With(ctx, channel)
}

// WithUserID injects user ID into context
func WithUserID(ctx context.Context, userId string) context.Context {
	return userIDKey.With(ctx, userId)
}

// NewSessionContext creates a context with session information
func NewSessionContext(ctx context.Context, sm SessionManager, req SessionRequest) (context.Context, *Session, error) {
	// Generate session key
	key := SessionKeyFromRequest(&req)

	// Get or create session
	session, err := sm.GetOrCreate(ctx, req)
	if err != nil {
		return ctx, nil, err
	}

	// Inject into context
	ctx = WithSession(ctx, session)
	ctx = WithSessionKey(ctx, key)
	ctx = WithSessionManager(ctx, sm)
	ctx = WithAgentID(ctx, req.AgentID)
	ctx = WithChannel(ctx, req.Channel)
	ctx = WithUserID(ctx, req.UserID)

	return ctx, session, nil
}
