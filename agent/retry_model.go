package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
	aierrors "github.com/rulego/rulego-components-ai/errors"
	"github.com/rulego/rulego/api/types"
)

// DefaultStreamProbeChunks The default size of the streaming probe window (number of chunks).
// After the stream is created, it prefetchs as many chunks to catch early interruptions (such as "Error in input stream"):
// If the flow is cut off within the window, it will be retested; If the flow is cut off after passing the window, it will be transmitted directly without retrying.
// The larger the value, the greater the coverage of early interruptions, but the greater the initial latency.
const DefaultStreamProbeChunks = 3

// RetryChatModelWrapper wraps ChatModel to provide retry functionality
type RetryChatModelWrapper struct {
	model.ToolCallingChatModel
	maxRetries  int
	probeChunks int  // Flow detection window size, set to default value when <=0 (used in off mode)
	streamFull  bool // Full mid-stream retry: Full buffer + retry + replay when true (sacrificing real-time)
	logger      types.Logger
}

// NewRetryChatModelWrapper creates a ChatModel wrapper with retry functionality
func NewRetryChatModelWrapper(baseModel model.ToolCallingChatModel, maxRetries int, logger ...types.Logger) *RetryChatModelWrapper {
	if maxRetries <= 0 {
		maxRetries = config.DefaultMaxRetries
	}
	var log types.Logger
	if len(logger) > 0 && logger[0] != nil {
		log = logger[0]
	}
	return &RetryChatModelWrapper{
		ToolCallingChatModel: baseModel,
		maxRetries:           maxRetries,
		probeChunks:          DefaultStreamProbeChunks,
		logger:               log,
	}
}

// SetProbeChunks sets the size of the stream probe window (number of chunks).
// When n<=0, the default value is restored; When set to 1, it regresses to the old behavior of "only detecting the first chunk."
// Used to adjust the detection intensity of early current interruption without altering structural parameters.
func (w *RetryChatModelWrapper) SetProbeChunks(n int) {
	if n > 0 {
		w.probeChunks = n
	} else {
		w.probeChunks = DefaultStreamProbeChunks
	}
}

// SetStreamFull Switches streaming retry mode: true=full mid-stream retry (buffered replay, sacrificing real-time),
// false = retry only within the detection window (off, keep real-time). From CreateChatModel, click config.StreamRetryMode settings.
func (w *RetryChatModelWrapper) SetStreamFull(full bool) {
	w.streamFull = full
}

// logWarnf warning logs (error/disconnection/retry exhaustion, key troubleshooting, total output)
func (w *RetryChatModelWrapper) logWarnf(format string, v ...interface{}) {
	if w.logger != nil {
		w.logger.Warnf(format, v...)
	}
}

// logInfof retries ongoing logs. Use Debug level: Retry is an exception path, production info does not disturb, and debug troubleshooting is visible.
func (w *RetryChatModelWrapper) logInfof(format string, v ...interface{}) {
	if w.logger != nil {
		w.logger.Debugf(format, v...)
	}
}

// IsRetryableError checks whether the error can be retried (package level, shared by retry/failover ShouldRetry/ShouldFailover).
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// First, check if it is of the AgentError type
	if aierrors.IsRetryable(err) {
		return true
	}

	// Check if it is a specific AgentError error code
	if aierrors.IsCode(err, aierrors.CodeLLMRateLimit) ||
		aierrors.IsCode(err, aierrors.CodeLLMTimeout) ||
		aierrors.IsCode(err, aierrors.CodeServiceUnavailable) {
		return true
	}

	errStr := err.Error()

	// 429 rate limiting error (containsHTTPStatus to prevent misjudgments of 429 in strings like timestamps/UUIDs)
	if containsHTTPStatus(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "Too Many Requests") {
		return true
	}

	// 5xx Server Error (containsHTTPStatus to avoid misjudgments of 500/502/503/504 in port/count numbers)
	if containsHTTPStatus(errStr, "500") ||
		containsHTTPStatus(errStr, "502") ||
		containsHTTPStatus(errStr, "503") ||
		containsHTTPStatus(errStr, "504") {
		return true
	}

	// Network errors
	if isNetworkError(err) {
		return true
	}

	// Stream response interrupt errors (such as "Error in input stream", unexpected EOF)
	if strings.Contains(errStr, "input stream") ||
		strings.Contains(errStr, "unexpected EOF") {
		return true
	}

	// Timeout error
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") {
		return true
	}

	return false
}

// isRetryableError reserved method (delegate package level IsRetryableError), backward compatible with existing calls and tests.
func (w *RetryChatModelWrapper) isRetryableError(err error) bool {
	return IsRetryableError(err)
}

// containsHTTPStatus checks whether the error string contains the specified HTTP status code.
// The status code must not be a numeric character before or after to avoid misjudgments in UUID and other strings.
func containsHTTPStatus(errStr string, code string) bool {
	for {
		idx := strings.Index(errStr, code)
		if idx < 0 {
			return false
		}
		// Check that the previous character is not a number
		if idx > 0 && errStr[idx-1] >= '0' && errStr[idx-1] <= '9' {
			errStr = errStr[idx+len(code):]
			continue
		}
		// After checking, the character is not a number
		afterIdx := idx + len(code)
		if afterIdx < len(errStr) && errStr[afterIdx] >= '0' && errStr[afterIdx] <= '9' {
			errStr = errStr[afterIdx:]
			continue
		}
		return true
	}
}

// isNetworkError checks whether it is a network error
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") {
		return true
	}
	return false
}

// calculateDelay calculates retry delay (exponential retreat + random jitter)
// First retry: ~1s, Second time: ~2s, Third time: ~4s, Fourth time: ~8s...
func (w *RetryChatModelWrapper) calculateDelay(attempt int) time.Duration {
	// Initial delay 1s, retreat factor 2
	const initialDelay = 1000 // ms
	const backoffFactor = 2
	const maxDelay = 30000 // ms

	delay := float64(initialDelay)
	for i := 1; i < attempt; i++ {
		delay *= float64(backoffFactor)
	}
	// Added random jitter (0.5x - 1.5x)
	jitter := 0.5 + rand.Float64()
	delay = delay * jitter

	// Maximum delay not exceeding 30 seconds
	if delay > float64(maxDelay) {
		delay = float64(maxDelay)
	}

	return time.Duration(delay) * time.Millisecond
}

// Generate is a method for generating with retrys
func (w *RetryChatModelWrapper) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	var lastErr error
	maxAttempts := w.maxRetries + 1 // +1 is because the first attempt doesn't count as a retry

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := w.ToolCallingChatModel.Generate(ctx, input, opts...)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// If it is not a retry error, it returns directly
		if !w.isRetryableError(err) {
			return nil, err
		}

		// If there is still a chance to retry, wait and then try again
		if attempt < maxAttempts {
			delay := w.calculateDelay(attempt)
			w.logInfof("[RetryChatModel] Generate attempt %d failed: %v, retrying in %v...", attempt, err, delay)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				continue
			}
		}
	}

	return nil, fmt.Errorf("Generate failed after %d attempts: %w", maxAttempts, lastErr)
}

// probeStream: The first n chunks of the prefetch stream, used to capture early disconnection after setup.
// Return nil: n chunks (stream health) successfully received, should be passed.
// Return io.EOF: Short current ends normally within n chunks and should be transparent.
// Returns other errors: Interruption in window, and the caller decides whether to retry based on retryability.
func probeStream(sr *schema.StreamReader[*schema.Message], n int) error {
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		if _, err := sr.Recv(); err != nil {
			return err
		}
	}
	return nil
}

// Stream generates streams with retrys. Retry during the establishment phase and within the "detection window" (the first probeChunks chunks);
// Once the stream passes the detection window and begins output to the caller, interruptions in the stream do not retry (to avoid repetition), and the transmission is transmitted directly.
// Copy(2) Shared cache: probe chunk detection inside the prefetch window, passCopy fully replays the entire stream from the first node (broadcast buffer, no data loss).
func (w *RetryChatModelWrapper) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	// Full mode: fully buffers the entire stream→ retries → successful retries (sacrificing real-time, replacing mid-stream with full retryability).
	if w.streamFull {
		return w.streamFullRetry(ctx, input, opts...)
	}
	maxAttempts := w.maxRetries + 1
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stream, err := w.ToolCallingChatModel.Stream(ctx, input, opts...)
		if err != nil {
			w.logWarnf("[RetryChatModel] Stream attempt %d open error: %v", attempt, err)
			// Stream establishment failure: No retry allowed direct transmission; retry records and waits for retry
			if !w.isRetryableError(err) {
				return nil, err
			}
			lastErr = err
			if attempt >= maxAttempts {
				break
			}
			w.logInfof("[RetryChatModel] Stream open attempt %d failed: %v, retrying...", attempt, err)
			if !w.sleep(ctx, w.calculateDelay(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}

		// Before probe probeChunks: Captures early interruptions before output to the frontend (e.g., "Error in input stream").
		// If there is no flow inside the window, try again; After passing through the window, the flow is cut off and transmitted directly, avoiding duplicate content.
		// The content preread by probe is fully replayed by passCopy (StreamReader.Copy serves as a broadcast buffer without data loss).
		copies := stream.Copy(2)
		probeCopy, passCopy := copies[0], copies[1]
		probeErr := probeStream(probeCopy, w.probeChunks)
		probeCopy.Close()
		if probeErr != nil && !errors.Is(probeErr, io.EOF) {
			w.logWarnf("[RetryChatModel] Stream attempt %d probe error (probeChunks=%d): %v", attempt, w.probeChunks, probeErr)
		}

		// No errors are found in the probe window (sufficient chunk received, or short-current normal EOF), and the call is handed over
		if probeErr == nil || errors.Is(probeErr, io.EOF) {
			return passCopy, nil
		}

		// Disconnect from the window, discard passCopy, and try again
		passCopy.Close()
		if !w.isRetryableError(probeErr) {
			return nil, probeErr
		}
		lastErr = probeErr
		if attempt >= maxAttempts {
			break
		}
		w.logInfof("[RetryChatModel] Stream probe attempt %d failed (in window): %v, retrying...", attempt, probeErr)
		if !w.sleep(ctx, w.calculateDelay(attempt)) {
			return nil, ctx.Err()
		}
	}

	if lastErr == nil {
		lastErr = errors.New("stream failed")
	}
	return nil, fmt.Errorf("Stream failed after %d attempts: %w", maxAttempts, lastErr)
}

// streamFullRetry Full mid-stream Retry Mode: Buffers each stream completely, retries if an error occurs, and replays after success.
// Sacrificing real-time performance (initial delay ≈ full generation time) in exchange for full retryability of mid-stream interruptions.
func (w *RetryChatModelWrapper) streamFullRetry(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	maxAttempts := w.maxRetries + 1
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stream, err := w.ToolCallingChatModel.Stream(ctx, input, opts...)
		if err != nil {
			w.logWarnf("[RetryChatModel] streamFull attempt %d/%d open error: %v", attempt, maxAttempts, err)
			if !IsRetryableError(err) {
				w.logWarnf("[RetryChatModel] streamFull attempt %d NOT retryable, return", attempt)
				return nil, err
			}
			lastErr = err
			if attempt >= maxAttempts {
				break
			}
			w.logInfof("[RetryChatModel] streamFull attempt %d retryable, retrying...", attempt)
			if !w.sleep(ctx, w.calculateDelay(attempt)) {
				return nil, ctx.Err()
			}
			continue
		}
		// Complete buffering of the entire flow to detect any interruptions midway
		chunks, streamErr := drainAndBufferStream(stream)
		if streamErr == nil {
			return streamReaderFromMessages(chunks), nil
		}
		w.logWarnf("[RetryChatModel] streamFull attempt %d/%d mid-stream error (chunks=%d): %v", attempt, maxAttempts, len(chunks), streamErr)
		if !IsRetryableError(streamErr) {
			w.logWarnf("[RetryChatModel] streamFull attempt %d NOT retryable, return", attempt)
			return nil, streamErr
		}
		lastErr = streamErr
		if attempt >= maxAttempts {
			break
		}
		w.logInfof("[RetryChatModel] streamFull attempt %d retryable, retrying...", attempt)
		if !w.sleep(ctx, w.calculateDelay(attempt)) {
			return nil, ctx.Err()
		}
	}
	w.logWarnf("[RetryChatModel] streamFull EXHAUSTED after %d attempts, lastErr=%v", maxAttempts, lastErr)
	if lastErr == nil {
		lastErr = errors.New("stream failed")
	}
	return nil, fmt.Errorf("Stream failed after %d attempts: %w", maxAttempts, lastErr)
}

// drainAndBufferStream consumes the entire stream to io.EOF (returns all chunk, nil) or error (returns received chunk, err).
func drainAndBufferStream(stream *schema.StreamReader[*schema.Message]) ([]*schema.Message, error) {
	defer stream.Close()
	var chunks []*schema.Message
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return chunks, nil
		}
		if err != nil {
			return chunks, err
		}
		if msg != nil {
			chunks = append(chunks, msg)
		}
	}
}

// streamReaderFromMessages constructs a replayable StreamReader from a buffered chunk.
func streamReaderFromMessages(chunks []*schema.Message) *schema.StreamReader[*schema.Message] {
	r, w := schema.Pipe[*schema.Message](0)
	go func() {
		defer w.Close()
		for _, c := range chunks {
			w.Send(c, nil)
		}
	}()
	return r
}

// Sleep waits for the specified duration, during which CTX cancels the response. Returning false means the CTX has been canceled.
func (w *RetryChatModelWrapper) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// WithTools setup tool
func (w *RetryChatModelWrapper) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	newModel, err := w.ToolCallingChatModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	// Return the packaged model to maintain retry capability. You must copy streamFull/probeChunks:
	// The Eino React binding tool must go through this path; if lost, full mode will silently degrade to OFF (streaming outside the window will not be retried if the stream is cut off).
	rw := NewRetryChatModelWrapper(newModel, w.maxRetries, w.logger)
	rw.streamFull = w.streamFull
	rw.probeChunks = w.probeChunks
	return rw, nil
}

// Ensure RetryChatModelWrapper implements model.ToolCallingChatModel
var _ model.ToolCallingChatModel = (*RetryChatModelWrapper)(nil)
