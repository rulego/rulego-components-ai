package constants

import "time"

// HTTP Client Timeouts
const (
	// Default HTTP request timeout in seconds
	DefaultHTTPTimeoutSec = 30

	// Default MCP connection timeout in seconds
	DefaultMCPTimeoutSec = 30

	// Default LLM request timeout in seconds
	DefaultLLMTimeoutSec = 60
)

// Redis Timeouts
const (
	// Default Redis dial timeout in seconds
	DefaultRedisDialTimeoutSec = 5

	// Default Redis read timeout in seconds
	DefaultRedisReadTimeoutSec = 3

	// Default Redis write timeout in seconds
	DefaultRedisWriteTimeoutSec = 3
)

// Session Timeouts
const (
	// Default session TTL in days
	DefaultSessionTTLDays = 30

	// Default session idle timeout in hours
	DefaultSessionIdleTimeoutHours = 1

	// Default cache TTL in minutes
	DefaultCacheTTLMinutes = 5
)

// Retry Configuration
const (
	// Default retry count
	DefaultRetryCount = 3

	// Default retry interval in milliseconds
	DefaultRetryIntervalMs = 1000
)

// Time duration helpers
var (
	DefaultHTTPTimeout      = time.Duration(DefaultHTTPTimeoutSec) * time.Second
	DefaultMCPTimeout       = time.Duration(DefaultMCPTimeoutSec) * time.Second
	DefaultLLMTimeout       = time.Duration(DefaultLLMTimeoutSec) * time.Second
	DefaultRedisDialTimeout = time.Duration(DefaultRedisDialTimeoutSec) * time.Second
	DefaultRedisReadTimeout = time.Duration(DefaultRedisReadTimeoutSec) * time.Second
	DefaultRedisWriteTimeout = time.Duration(DefaultRedisWriteTimeoutSec) * time.Second
	DefaultCacheTTL         = time.Duration(DefaultCacheTTLMinutes) * time.Minute
	DefaultSessionTTL       = time.Duration(DefaultSessionTTLDays) * 24 * time.Hour
	DefaultSessionIdleTimeout = time.Duration(DefaultSessionIdleTimeoutHours) * time.Hour
)
