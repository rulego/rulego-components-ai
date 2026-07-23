package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego/api/types"
)

// ===== Primary endpoint fuse =====

// maxCircuitCooldown detection evasion limit: After a half-open detection fails, the cooldown is gradually doubled, capped at this value.
// This avoids the incessant cooling growth during prolonged master terminal failures, which can cause delays in switching to the master after recovery.
const maxCircuitCooldown = 10 * time.Minute

// circuitState Fuse State Machine: closed→ open→ half-open (half-open probability).
type circuitState int32

const (
	circuitClosed   circuitState = iota // Normal, allowing the main endpoint to be tested
	circuitOpen                         // Fuse is skipped, skip the main one, and use it as a backup
	circuitHalfOpen                     // Half-open, test the master once
)

// circuitBreaker protects the fuse at the main endpoint. The primary endpoint is already RetryChatModelWrapper, so "one failure" means
// For the same model, retry exhaustion is exhausted, and circuit breaks do not require a cumulative threshold—if closed, the main failure after one failure directly switches to open.
//
// Behavior:
//   - closed: The master endpoint fails (retry is exhausted) → opens (cooldown with base cooldown).
//     Main success → keeps closed.
//   - open: allowPrimary returns false (skips the main and uses the backup directly). When the cooldown expires (now > openUntil), → switch to half-open.
//   - half-open: allowPrimary only allows "one" request to probe the main (halfOpenProbing occupiance), while the rest return false and goes as a backup.
//     Avoid triggering concurrent requests when cooldown expires to go all-in on the main character. Probing successfully: → closed (cooldown resets to base value); Failure → reopening,
//     Additionally, detection cooling is doubled and capped at maxCircuitCooldown (during continuous faults, detection frequency decreases sequentially to reduce unnecessary detection during the bad owner).
//     recordSuccess/recordFailure releases the tendency to do so.
//
// Thread safety.
type circuitBreaker struct {
	mu              sync.Mutex
	state           circuitState
	cooldown        time.Duration // Basic cooling (user-configured values)
	currentCooldown time.Duration // Current actual cooldown: doubles as detection fails, resets after successful detection and cooldown
	openUntil       time.Time
	halfOpenProbing bool // When half-open: true=Existing requests are testing the master, while other requests go to the standby
}

// newCircuitBreaker creates a fuse. When cooldown <=0, use the default value (60s).
func newCircuitBreaker(cooldown time.Duration) *circuitBreaker {
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	return &circuitBreaker{cooldown: cooldown, currentCooldown: cooldown}
}

// Does allowPrimary allow attempts on the primary endpoint? When the open expires, it will automatically switch to half-open.
// Under half-open, only one request is allowed to probe the master (halfOpenProbing), while the rest return false as a standby.
// Avoid triggering concurrent requests when cooldown expires to go all-in on the main character. The heuring result is released by recordSuccess/recordFailure.
func (c *circuitBreaker) allowPrimary() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case circuitClosed:
		return true
	case circuitOpen:
		if !time.Now().After(c.openUntil) {
			return false // During circuit breaking, skip the main one
		}
		c.state = circuitHalfOpen // When the cooldown ends, switch to half open
		fallthrough
	case circuitHalfOpen:
		// Only one request was allowed to test the master; the rest were kept on standby
		if !c.halfOpenProbing {
			c.halfOpenProbing = true
			return true
		}
		return false
	}
	return true
}

// recordSuccess The master endpoint succeeds → restores closed, resets cooldown to base values, and releases half-open testing rights.
func (c *circuitBreaker) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = circuitClosed
	c.currentCooldown = c.cooldown // Detection successful→ retreat and return to position
	c.halfOpenProbing = false
}

// recordFailure: The master endpoint failed (retry has been exhausted).
// half-open probe failure: After the cooldown doubles and caps, reopen (and release the probing right).
// Closed main failure below: First circuit break, use base cooling to switch to open.
func (c *circuitBreaker) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == circuitHalfOpen {
		c.currentCooldown = c.nextCooldown() // Detection failure → Cooldown doubles at cap
		c.state = circuitOpen
		c.openUntil = time.Now().Add(c.currentCooldown)
		c.halfOpenProbing = false // Release the right to test and re-circuit breaking
		return
	}
	c.state = circuitOpen
	c.currentCooldown = c.cooldown // The first circuit breaker uses foundation cooling
	c.openUntil = time.Now().Add(c.currentCooldown)
}

// nextCooldown calculates the next cooldown after detection failure: current cooldown doubles and maxCircuitCooldown is capped.
func (c *circuitBreaker) nextCooldown() time.Duration {
	if doubled := c.currentCooldown * 2; doubled < maxCircuitCooldown {
		return doubled
	}
	return maxCircuitCooldown
}

// onPrimaryNonFailoverError Handling when the primary endpoint returns a non-failover error (such as a 400 request formatting error).
// Closed state: not fused (a client-side class error does not mean the main endpoint is unavailable).
// half-open state: detection fails, must advance the state machine—release detection rights and reopen (use backup),
// Otherwise, halfOpenProbing will never be released, and the state will permanently be stuck at half-open (the main endpoint will be frozen). Cooldown does not double the current value separately.
func (c *circuitBreaker) onPrimaryNonFailoverError() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != circuitHalfOpen {
		return // Closed state: Client errors are not circuited
	}
	c.state = circuitOpen
	c.openUntil = time.Now().Add(c.currentCooldown)
	c.halfOpenProbing = false // Release the right to probe, reopen and go for backup
}

// getState returns the current state (for log/test purposes).
func (c *circuitBreaker) getState() circuitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// IsFailoverError determines whether the error should trigger failover (cuts to the standby endpoint).
// More lenient than IsRetryableError: In addition to retry errors, authentication/authorization errors (401/403/invalid_api_key) are also triggered—
// Backup endpoints using different keys/URLs may succeed in authentication; Retry, on the other hand, is meaningless for authentication errors when retrying the same model, so the two judgments are separated.
func IsFailoverError(err error) bool {
	if err == nil {
		return false
	}
	if IsRetryableError(err) {
		return true
	}
	errStr := err.Error()
	if containsHTTPStatus(errStr, "401") ||
		containsHTTPStatus(errStr, "403") ||
		strings.Contains(errStr, "Unauthorized") ||
		strings.Contains(errStr, "invalid_api_key") ||
		strings.Contains(errStr, "invalid api key") {
		return true
	}
	return false
}

// ===== FailoverChatModelWrapper =====

// FailoverChatModelWrapper: After the primary endpoint fails (retry error or 401/403 authentication error), the alternate endpoint is switched sequentially.
//
// Primary and failovers are usually each already *RetryChatModelWrapper (retries within the same model first, and failovers are triggered only after exhaustion).
// This creates a complete chain of "retry (same model) → failover (cut model)."
//
// Optional fuse (WithCircuit enabled): Fuse blows after master fails consecutively and reaches the threshold; during cooldown, skip the main and use it directly as a backup.
// This prevents each request from waiting for the master retry to exhaust during long-term master failures.
//
// Behavior is related to streaming patterns:
//   - Generate: Any endpoint returns a retry error or authentication error → Cut off one; Requests and formats that cannot be switched are directly returned.
//   - Stream(off mode): The endpoint stream returns an error (establishment failed).
//     Once a stream on an endpoint successfully returns to the reader, subsequent mid-stream errors are propagated by that reader and no longer failover.
//   - Stream (full mode): The endpoint stream is fully buffered and retries internally; mid-stream returns an error only when the stream is exhausted → cuts to the next one.
type FailoverChatModelWrapper struct {
	primary   model.ToolCallingChatModel
	failovers []model.ToolCallingChatModel
	circuit   *circuitBreaker // Primary endpoint fuse, nil means disabled (test the main each time)
	logger    types.Logger
}

// NewFailoverChatModelWrapper creates a failover wrapper. failovers is the equivalent of using a primary when empty.
func NewFailoverChatModelWrapper(primary model.ToolCallingChatModel, failovers []model.ToolCallingChatModel, logger ...types.Logger) *FailoverChatModelWrapper {
	var log types.Logger
	if len(logger) > 0 && logger[0] != nil {
		log = logger[0]
	}
	return &FailoverChatModelWrapper{primary: primary, failovers: failovers, logger: log}
}

// WithCircuit enables the master endpoint fuse (builder mode). cooldown: Cooling duration; <=0 uses the default (60s).
func (w *FailoverChatModelWrapper) WithCircuit(cooldown time.Duration) *FailoverChatModelWrapper {
	w.circuit = newCircuitBreaker(cooldown)
	return w
}

func (w *FailoverChatModelWrapper) logf(format string, v ...interface{}) {
	if w.logger != nil {
		w.logger.Printf(format, v...)
	}
}

// models: Returns the primary + backup endpoint model by priority.
func (w *FailoverChatModelWrapper) models() []model.ToolCallingChatModel {
	all := make([]model.ToolCallingChatModel, 0, 1+len(w.failovers))
	all = append(all, w.primary)
	all = append(all, w.failovers...)
	return all
}

// startIdx calculation should start from which endpoint (skip the main when the circuit breaker is on and the master is open).
// Returns (startIdx, triedPrimary). triedPrimary indicates whether the main (used for fuse count) was actually tested.
func (w *FailoverChatModelWrapper) startIdx() (int, bool) {
	if w.circuit == nil {
		return 0, true
	}
	if !w.circuit.allowPrimary() {
		w.logf("[FailoverChatModel] primary circuit open, skipping to failover endpoints")
		return 1, false
	}
	return 0, true
}

// Generate synchronous generation: tries each endpoint by priority, allowing retry errors to trigger failover; The master endpoint's success/failure feedback is sent to the fuse.
func (w *FailoverChatModelWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	models := w.models()
	idx, tryPrimary := w.startIdx()
	if idx > 0 && len(models) <= 1 {
		return nil, fmt.Errorf("primary circuit open and no failover endpoints available")
	}

	var lastErr error
	for i := idx; i < len(models); i++ {
		msg, err := models[i].Generate(ctx, input, opts...)
		if err == nil {
			if i == 0 && tryPrimary && w.circuit != nil {
				w.circuit.recordSuccess()
			}
			if i > 0 {
				w.logf("[FailoverChatModel] Generate failed over to endpoint #%d after error: %v", i, lastErr)
			}
			return msg, nil
		}
		isFailover := IsFailoverError(err)
		if i == 0 && tryPrimary && w.circuit != nil {
			if isFailover {
				w.circuit.recordFailure() // Failover error: Circuit break/ejection
			} else {
				w.circuit.onPrimaryNonFailoverError() // half-open: release detection rights and reopen again; closed, not fused
			}
		}
		if !isFailover {
			return nil, err // Non-failover errors (such as request format errors) are returned directly without switching
		}
		lastErr = err
		w.logf("[FailoverChatModel] Generate endpoint #%d failed: %v, trying next...", i, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available model")
	}
	return nil, fmt.Errorf("Generate failed over all endpoints: %w", lastErr)
}

// Stream generation: Try each endpoint in priority. The endpoint stream returns an error (including retry exhaustion) before switching;
// Returning reader means the endpoint has already taken over the current stream, and subsequent mid-stream errors are propagated.
func (w *FailoverChatModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	models := w.models()
	idx, tryPrimary := w.startIdx()
	if idx > 0 && len(models) <= 1 {
		return nil, fmt.Errorf("primary circuit open and no failover endpoints available")
	}

	var lastErr error
	for i := idx; i < len(models); i++ {
		stream, err := models[i].Stream(ctx, input, opts...)
		if err == nil {
			if i == 0 && tryPrimary && w.circuit != nil {
				w.circuit.recordSuccess()
			}
			if i > 0 {
				w.logf("[FailoverChatModel] Stream failed over to endpoint #%d after error: %v", i, lastErr)
			}
			return stream, nil
		}
		isFailover := IsFailoverError(err)
		if i == 0 && tryPrimary && w.circuit != nil {
			if isFailover {
				w.circuit.recordFailure() // Failover error: Circuit break/ejection
			} else {
				w.circuit.onPrimaryNonFailoverError() // half-open: release detection rights and reopen again; closed, not fused
			}
		}
		if !isFailover {
			return nil, err // Non-failover errors are returned directly without switching
		}
		lastErr = err
		w.logf("[FailoverChatModel] Stream endpoint #%d failed: %v, trying next...", i, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no available model")
	}
	return nil, fmt.Errorf("Stream failed over all endpoints: %w", lastErr)
}

// WithTools binds tools for all endpoints and returns a new wrapper that maintains the failover structure.
// Fuse pointer sharing allows continuous failure accumulation across WithTools—avoiding the hassle of resetting every tool binding that renders the fuse useless.
func (w *FailoverChatModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newPrimary, err := w.primary.WithTools(tools)
	if err != nil {
		return nil, err
	}
	newFailovers := make([]model.ToolCallingChatModel, len(w.failovers))
	for i, f := range w.failovers {
		nf, err := f.WithTools(tools)
		if err != nil {
			return nil, err
		}
		newFailovers[i] = nf
	}
	fo := NewFailoverChatModelWrapper(newPrimary, newFailovers, w.logger)
	fo.circuit = w.circuit // Shared fuse status
	return fo, nil
}

// Ensure FailoverChatModelWrapper implements model.ToolCallingChatModel
var _ model.ToolCallingChatModel = (*FailoverChatModelWrapper)(nil)

// fixedModelWrapper: Forces the configured model name and ignores the WithModel override passed from the upper layer
// (For example, session-level session_model). For failover backup endpoints—session-level models are selected by users for the main provider,
// The backup provider may not support the same model name; forcibly bringing it over will trigger "Model does not exist".
// Backup fixes using your own configuration model name to ensure failover is available.
type fixedModelWrapper struct {
	base       model.ToolCallingChatModel
	fixedModel string
}

// append WithModel (fixedModel) to the end: apply executes in order, and the WithModel written later overrides the one written first
// (i.e., override the session_model injected in the upper layer), so that the underlying layer uses fixedModel.
func (w *fixedModelWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return w.base.Generate(ctx, input, append(opts, model.WithModel(w.fixedModel))...)
}

func (w *fixedModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return w.base.Stream(ctx, input, append(opts, model.WithModel(w.fixedModel))...)
}

func (w *fixedModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	nb, err := w.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &fixedModelWrapper{base: nb, fixedModel: w.fixedModel}, nil
}

var _ model.ToolCallingChatModel = (*fixedModelWrapper)(nil)
