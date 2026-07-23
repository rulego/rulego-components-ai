package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/rulego/rulego-components-ai/config"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/utils/maps"
	"github.com/stretchr/testify/require"
)

// endpointModel controllable endpoint mock, supporting preset Generate/Stream behaviors for failover testing.
type endpointModel struct {
	name        string
	genResult   *schema.Message
	genErr      error
	stream      *schema.StreamReader[*schema.Message]
	streamErr   error
	genCalls    int
	streamCalls int
}

func (m *endpointModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.genCalls++
	if m.genErr != nil {
		return nil, m.genErr
	}
	if m.genResult != nil {
		return m.genResult, nil
	}
	return schema.AssistantMessage(m.name, nil), nil
}

func (m *endpointModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.streamCalls++
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	if m.stream != nil {
		return m.stream, nil
	}
	return streamReaderFromChunks(m.name), nil
}

func (m *endpointModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// TestFailover_GeneratePrimaryFailsToBackup The master endpoint Generate can retry but fails, → switch to backup successfully.
func TestFailover_GeneratePrimaryFailsToBackup(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("status code: 502 bad gateway")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	msg, err := w.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expect success after failover, got err: %v", err)
	}
	if msg.Content != "OK" {
		t.Fatalf("Expected content: OK, got %s", msg.Content)
	}
	if backup.genCalls != 1 {
		t.Fatalf("Backup endpoints should be called once got %d", backup.genCalls)
	}
}

// TestFailover_GenerateAllFail All endpoints fail → Summary error.
func TestFailover_GenerateAllFail(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")}
	backup := &endpointModel{name: "backup", genErr: errors.New("503")}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	_, err := w.Generate(context.Background(), nil)
	if err == nil {
		t.Fatal("Expect all failures to report errors")
	}
	if !strings.Contains(err.Error(), "failed over all endpoints") {
		t.Fatalf("Expect to summarize errors and got: %v", err)
	}
}

// TestFailover_GenerateNonRetryableNoSwitch Request format class error (400)→ Not switching to backup:
// Backup endpoints will fail if they receive the same request, making the switch meaningless. Note: Authentication errors (401/invalid_api_key) are not included in this category,
// It will be overridden by TestFailover_AuthErrorSwitchesToBackup—the backup endpoint uses a different key, and authentication may succeed.
func TestFailover_GenerateNonRetryableNoSwitch(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("400 Bad Request: invalid message")}
	backup := &endpointModel{name: "backup"}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	_, err := w.Generate(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("Expected transparenting request format is incorrect, got: %v", err)
	}
	if backup.genCalls != 0 {
		t.Fatalf("If the request format is incorrect, the switch should not be made; the standby is called %d times", backup.genCalls)
	}
}

// TestFailover_AuthErrorSwitchesToBackup Authentication error (401/invalid_api_key) → Switch to backup:
// Retry: Retrying the same model is meaningless for authentication errors (no retry), but failover is worth trying to switch to alternate endpoints with different keys/URLs.
func TestFailover_AuthErrorSwitchesToBackup(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("401 Unauthorized: invalid_api_key")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	msg, err := w.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Authentication errors should be failover until backup success got err: %v", err)
	}
	if msg.Content != "OK" {
		t.Fatalf("Expect the backup to return OK,got %s", msg.Content)
	}
	if backup.genCalls != 1 {
		t.Fatalf("Authentication errors should switch to a backup, which is called %d times", backup.genCalls)
	}
}

// TestFailover_StreamPrimaryFailsToBackup Primary endpoint Stream fails → Backup succeeds.
func TestFailover_StreamPrimaryFailsToBackup(t *testing.T) {
	primary := &endpointModel{name: "primary", streamErr: errors.New("Error in input stream")}
	backup := &endpointModel{name: "backup", stream: streamReaderFromChunks("OK")}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expect success after failover, got err: %v", err)
	}
	contents, recvErr := drainStream(sr)
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("Expect io.EOF, got: %v", recvErr)
	}
	if len(contents) != 1 || contents[0] != "OK" {
		t.Fatalf("Expect [OK], got %v", contents)
	}
	if backup.streamCalls != 1 {
		t.Fatalf("Backup endpoints Stream should be called once got %d", backup.streamCalls)
	}
}

// TestFailover_StreamAllFail All endpoint streams fail → aggregation errors.
func TestFailover_StreamAllFail(t *testing.T) {
	primary := &endpointModel{name: "primary", streamErr: errors.New("502")}
	backup := &endpointModel{name: "backup", streamErr: errors.New("503")}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	sr, err := w.Stream(context.Background(), nil)
	if sr != nil {
		sr.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "failed over all endpoints") {
		t.Fatalf("Expect to summarize errors and got: %v", err)
	}
}

// TestFailover_NoFailoverEqualsPrimary When there are no spare endpoints, it is equivalent to using Primary directly.
func TestFailover_NoFailoverEqualsPrimary(t *testing.T) {
	primary := &endpointModel{name: "primary", genResult: schema.AssistantMessage("P", nil)}
	w := NewFailoverChatModelWrapper(primary, nil)

	msg, err := w.Generate(context.Background(), nil)
	if err != nil || msg.Content != "P" {
		t.Fatalf("Expect primary to return P directly, got msg = %v err = %v", msg, err)
	}
}

// TestFailover_GeneratePrimarySuccessNoSwitch The master endpoint succeeds→ No switching to the standby.
func TestFailover_GeneratePrimarySuccessNoSwitch(t *testing.T) {
	primary := &endpointModel{name: "primary", genResult: schema.AssistantMessage("OK", nil)}
	backup := &endpointModel{name: "backup"}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	msg, err := w.Generate(context.Background(), nil)
	if err != nil || msg.Content != "OK" {
		t.Fatalf("Expect primary to return OK directly, got msg = %v err = %v", msg, err)
	}
	if backup.genCalls != 0 {
		t.Fatalf("When the master succeeds, backup should not be called up, got %d", backup.genCalls)
	}
}

// TestFailover_GenerateMultiBackup Main + Standby 1 fails → Backup 2 succeeds (switching multiple endpoints sequentially).
func TestFailover_GenerateMultiBackup(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")}
	backup1 := &endpointModel{name: "backup1", genErr: errors.New("503")}
	backup2 := &endpointModel{name: "backup2", genResult: schema.AssistantMessage("OK2", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup1, backup2})

	msg, err := w.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expect failover to backup2 success, got err: %v", err)
	}
	if msg.Content != "OK2" {
		t.Fatalf("Expect OK2, got %s", msg.Content)
	}
	if backup1.genCalls != 1 || backup2.genCalls != 1 {
		t.Fatalf("Expect backup1/backup2 to call once each, got %d/%d", backup1.genCalls, backup2.genCalls)
	}
}

// TestFailover_StreamPrimarySuccessNoSwitch The master endpoint stream succeeds→ without switching to the standby.
func TestFailover_StreamPrimarySuccessNoSwitch(t *testing.T) {
	primary := &endpointModel{name: "primary", stream: streamReaderFromChunks("OK")}
	backup := &endpointModel{name: "backup"}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})

	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expect primary to succeed directly and got err: %v", err)
	}
	contents, _ := drainStream(sr)
	if len(contents) != 1 || contents[0] != "OK" {
		t.Fatalf("Expect [OK], got %v", contents)
	}
	if backup.streamCalls != 0 {
		t.Fatalf("Backup Stream should not be called when the master succeeds, got %d", backup.streamCalls)
	}
}

// ===== Fuse (circuit breaker) testing =====

// TestCircuit_OpensAndSkipsPrimary Main retry: When exhausted, the circuit breaks → open →. Skip the main and use it as a backup for subsequent uses.
func TestCircuit_OpensAndSkipsPrimary(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")} // The Lord continues to fail
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}).
		WithCircuit(50 * time.Millisecond) // Cooling time: 50ms

	// First attempt: The main subject fails (retry exhausted) → Backup succeeds. The main subject is open after one try.
	msg, err := w.Generate(context.Background(), nil)
	if err != nil || msg.Content != "OK" {
		t.Fatalf("The first attempt should failover successful, got msg = %v err = %v", msg, err)
	}
	if primary.genCalls != 1 {
		t.Fatalf("The main response should be fused after one test and got %d", primary.genCalls)
	}

	// Second time: The master is open→ skipping the main and directly using the standby. The Lord will no longer be tested.
	primary.genCalls = 0
	msg, err = w.Generate(context.Background(), nil)
	if err != nil || msg.Content != "OK" {
		t.Fatalf("After fuse, directly use backup, got msg = %v err = %v", msg, err)
	}
	if primary.genCalls != 0 {
		t.Fatalf("After the fuse, the main should be skipped, but the main subject should be tested %d times", primary.genCalls)
	}
}

// TestCircuit_HalfOpenRecovery open cooldown expires → half-open → probe the main successful → restore closed.
func TestCircuit_HalfOpenRecovery(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}).
		WithCircuit(50 * time.Millisecond)

	// One failure → open
	w.Generate(context.Background(), nil)

	time.Sleep(60 * time.Millisecond) // Cooldown expires → half-open

	// Let the master succeed this time (simulate the master's recovery)
	primary.genErr = nil
	primary.genResult = schema.AssistantMessage("PRIMARY", nil)
	msg, err := w.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("half-open The master should return to the got err: %v after success", err)
	}
	if msg.Content != "PRIMARY" {
		t.Fatalf("half-open Candidates and candidates succeed in got %s", msg.Content)
	}
	// After restoring closed, next request will be made directly as the main one
	primary.genCalls = 0
	w.Generate(context.Background(), nil)
	if primary.genCalls != 1 {
		t.Fatalf("After restoring closed, the test subject should be directly got %d", primary.genCalls)
	}
}

// TestCircuit_HalfOpenReOpensOnFailure half-open Testing the master fails → Re-open (continues to skip the master).
func TestCircuit_HalfOpenReOpensOnFailure(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")} // The Lord continues to fail
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}).
		WithCircuit(50 * time.Millisecond)

	// One failure → open
	w.Generate(context.Background(), nil)

	time.Sleep(60 * time.Millisecond) // Cooldown expires → half-open

	// half-open tries to master once; if the master still fails→ reopen
	primary.genCalls = 0
	w.Generate(context.Background(), nil)
	if primary.genCalls != 1 {
		t.Fatalf("half-open The examinee is the main candidate once, got %d", primary.genCalls)
	}
	// Reopen, next time skip the main page
	primary.genCalls = 0
	w.Generate(context.Background(), nil)
	if primary.genCalls != 0 {
		t.Fatalf("half-open After re-open failure, skip the main got %d", primary.genCalls)
	}
}

// TestCircuit_DisabledAlwaysTriesPrimary Fuses are not enabled (no WithCircuit) → Trial master for each request.
func TestCircuit_DisabledAlwaysTriesPrimary(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}) // No fuse breaking

	for i := 0; i < 5; i++ {
		w.Generate(context.Background(), nil)
	}
	if primary.genCalls != 5 {
		t.Fatalf("No fuse should be tested on each request, expecting 5 times to got %d", primary.genCalls)
	}
}

// TestFailover_WithTools_SharesCircuit After WithTools, fuse state sharing continues:
// When the master has fused open, the new wrapper generated by WithTools still skips the master (it will not retry the master due to resetting).
// Regression prevention: In the original implementation, WithTools would reset with newCircuitBreaker, causing eino react to break and fail every time a tool was bound.
func TestFailover_WithTools_SharesCircuit(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("status code: 502 bad gateway")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup})
	w = w.WithCircuit(time.Hour) // Cooling: 1 hour (keep open during testing)

	// Trigger the main failure once→ Circuit breaker open
	_, _ = w.Generate(context.Background(), nil)
	callsBefore := primary.genCalls

	// WithTools binding tools (eino react does this every time Stream)
	w2, err := w.WithTools(nil)
	if err != nil {
		t.Fatalf("WithTools Failure: %v", err)
	}

	// New wrapper generate: Fuse sharing should still be open → skip the main direct backup
	_, err = w2.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Expect failover to backup success, got err: %v", err)
	}
	if primary.genCalls != callsBefore {
		t.Errorf("WithTools After the fuse, it should be shared: the master should not be called (open), but the genCalls should increase from %d to %d",
			callsBefore, primary.genCalls)
	}
}

// TestCircuit_HalfOpenAllowsOnlyOneProbe During the half-open period, only one request is allowed to test the master; the rest are kept as backups.
// Anti-regression: Avoid triggering all concurrent requests to the main player who has just recovered (or not) the moment the cooldown expires.
func TestCircuit_HalfOpenAllowsOnlyOneProbe(t *testing.T) {
	c := newCircuitBreaker(time.Hour)
	// Directly construct open and have been overcooled→ next time allowPrimary switches to half-open
	c.state = circuitOpen
	c.openUntil = time.Now().Add(-time.Second)

	// First request: half-open + Occupy Testing Rights → Release the Subject
	if !c.allowPrimary() {
		t.Fatal("half-open The first request should be allowed to test the subject")
	}
	if c.getState() != circuitHalfOpen {
		t.Fatalf("It should be in half-open got %v", c.getState())
	}
	// Follow-up request: Probing rights have been taken → Refused (takes backup)
	for i := 0; i < 5; i++ {
		if c.allowPrimary() {
			t.Fatalf("During half-open, only one request should be granted; the %d additional request is mistakenly granted", i+2)
		}
	}

	// Probing successful→ Release probing rights + Restore closed
	c.recordSuccess()
	if c.getState() != circuitClosed {
		t.Fatalf("If the probe succeeds, closed and got %v should be restored", c.getState())
	}
	// After restoration, the main user is allowed
	if !c.allowPrimary() {
		t.Fatal("closed Allow the main author")
	}
}

// TestCircuit_HalfOpenProbeReleasedOnFailure half-open Testing fails to release the Testing Right; the next cooldown expires and you can test again.
func TestCircuit_HalfOpenProbeReleasedOnFailure(t *testing.T) {
	c := newCircuitBreaker(time.Hour)
	c.state = circuitHalfOpen
	c.halfOpenProbing = true // Simulations already have requests being probed

	c.recordFailure() // If the test fails→ reopen + release the right to test
	if c.getState() != circuitOpen {
		t.Fatal("half-open If probing fails, open again")
	}
	// After cooldown expires, you should be able to seize the test again (proving the right to test has been released).
	c.openUntil = time.Now().Add(-time.Second)
	if !c.allowPrimary() {
		t.Fatal("After the right to probing is released, the cooldown should expire and you should be able to seize the test again")
	}
}

// TestCircuit_ProbeBackoff half-open detection failure → Cooldown doubles and caps repeatedly; Detection successful→ Resets base cooling.
func TestCircuit_ProbeBackoff(t *testing.T) {
	base := 60 * time.Second
	c := newCircuitBreaker(base)
	if c.currentCooldown != base {
		t.Fatalf("The initial cooling should be a base value of %v, got %v", base, c.currentCooldown)
	}

	// closed first failure → open, cooled down by the base
	c.recordFailure()
	if c.currentCooldown != base {
		t.Fatalf("The first fuse cooling should be at the base value of %v, got %v", base, c.currentCooldown)
	}

	// Simulated detection consecutive failures: each half-open failure doubles the cooldown.
	// Sequence: 60s → 120s → 240s → 480s → 960s → Capped maxCircuitCooldown (10m).
	want := base
	for step := 0; step < 4; step++ {
		c.state = circuitHalfOpen // Forced entry into detection mode
		c.recordFailure()
		want *= 2
		if want > maxCircuitCooldown {
			want = maxCircuitCooldown
		}
		if c.currentCooldown != want {
			t.Fatalf("After the %d detection failure, cooling should be %v got %v", step+1, want, c.currentCooldown)
		}
	}

	// If you fail again, the limit should be capped at maxCircuitCooldown
	for i := 0; i < 3; i++ {
		c.state = circuitHalfOpen
		c.recordFailure()
		if c.currentCooldown != maxCircuitCooldown {
			t.Fatalf("Cooling should be capped at %v and got %v", maxCircuitCooldown, c.currentCooldown)
		}
	}

	// Detection successful→ Resets base cooling
	c.state = circuitHalfOpen
	c.recordSuccess()
	if c.currentCooldown != base {
		t.Fatalf("After successful detection, cooling should be reset to the base value %v, got %v", base, c.currentCooldown)
	}
}

// TestCircuit_DefaultCooldown newCircuitBreaker(0) uses default cooling of 60s.
func TestCircuit_DefaultCooldown(t *testing.T) {
	c := newCircuitBreaker(0)
	if c.cooldown != 60*time.Second {
		t.Fatalf("The default cooling should be 60s, got %v", c.cooldown)
	}
	if c.currentCooldown != 60*time.Second {
		t.Fatalf("The initial currentCooldown should be 60s, got %v", c.currentCooldown)
	}
}

// TestCircuit_NoFailoverOnClientError The main return is a non-failover error (such as a format error in the 400 request). It should not be melted or switched, and should return directly.
// Anti-regression: After removing the threshold, a single failure results in a circuit breaker. Only failover errors are counted as circuit breakers.
func TestCircuit_NoFailoverOnClientError(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("400 bad request: invalid model")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}).
		WithCircuit(time.Hour)

	// Main return 400 (not a failover error) → Returns an error directly, no circuit breaker or backup error
	_, err := w.Generate(context.Background(), nil)
	if err == nil {
		t.Fatal("The main return 400 should return an error directly")
	}
	if primary.genCalls != 1 {
		t.Fatalf("The main subject is tested once, got %d", primary.genCalls)
	}
	// Fuse should not be open: Next time the request is still made (not skipped)
	primary.genCalls = 0
	w.Generate(context.Background(), nil)
	if primary.genCalls != 1 {
		t.Fatalf("Non-failover errors should not be fused; next time, the test subject should still be got %d", primary.genCalls)
	}
}

// TestCircuit_HalfOpenProbeNonFailoverError When detecting a non-failover error (such as 400) in half-open detection,
// Release detection rights and reopen (go for backup), avoid getting stuck in half-open.
// Regression Protection: Section B caused probe rights leakage → halfOpenProbing never to release → permanently frozen the primary endpoint.
func TestCircuit_HalfOpenProbeNonFailoverError(t *testing.T) {
	primary := &endpointModel{name: "primary", genErr: errors.New("502")}
	backup := &endpointModel{name: "backup", genResult: schema.AssistantMessage("OK", nil)}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}).
		WithCircuit(50 * time.Millisecond)

	// The main 502 → one failure means open
	w.Generate(context.Background(), nil)
	time.Sleep(60 * time.Millisecond) // Cooldown expires → half-open

	// half-open detection: main return 400 (not failover)→ This time it directly returns an error (no switching),
	// But onPrimaryNonFailoverError releases detection rights and reopens (key: does not freeze).
	primary.genErr = errors.New("400 bad request: invalid model")
	primary.genCalls = 0
	_, err := w.Generate(context.Background(), nil)
	if err == nil {
		t.Fatal("half-open When detecting 400, it should return an error directly (do not switch unless failover)")
	}
	if primary.genCalls != 1 {
		t.Fatalf("half-open Candidate 1 time, got %d", primary.genCalls)
	}

	// Next request: reopen before expiration → skip the main and take the backup (prove not stuck in half-open)
	primary.genCalls = 0
	msg, err := w.Generate(context.Background(), nil)
	if err != nil || msg.Content != "OK" {
		t.Fatalf("After re-open at 400, the detection should failover to backup, got msg=%v err=%v", msg, err)
	}
	if primary.genCalls != 0 {
		t.Fatalf("After re-open, the main subject should be skipped, but the main subject %d times (detection rights leakage?).)", primary.genCalls)
	}
}

// TestCircuit_StreamOpensAndSkipsPrimary Stream path circuit break: the main retry is exhausted and the circuit breaks → open → subsequent streams skip the main circuit.
// Covers circuit breaker behavior in Stream and Generate image codes (anti-regression: only one of two image codes can be changed).
func TestCircuit_StreamOpensAndSkipsPrimary(t *testing.T) {
	primary := &endpointModel{name: "primary", streamErr: errors.New("502")}
	backup := &endpointModel{name: "backup"}
	w := NewFailoverChatModelWrapper(primary, []model.ToolCallingChatModel{backup}).
		WithCircuit(50 * time.Millisecond)

	// First stream: Main failure → Backup succeeded. The main subject is open after one try.
	sr, err := w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("The first Stream should failover successful, got err: %v", err)
	}
	drainStream(sr)
	if primary.streamCalls != 1 {
		t.Fatalf("The main one should be Stream tested once before the fuse is got %d", primary.streamCalls)
	}

	// Second stream: The master has opened → skipped the main and directly made a standby. The Lord will no longer be tested.
	primary.streamCalls = 0
	sr, err = w.Stream(context.Background(), nil)
	if err != nil {
		t.Fatalf("After the fuse, it should be Stream backup directly and got err: %v", err)
	}
	drainStream(sr)
	if primary.streamCalls != 0 {
		t.Fatalf("After the circuit breaks, the main should be skipped, but the main Stream should be tested %d times", primary.streamCalls)
	}
}

// TestApplyFailoverEndpoint_ParamsOverride Validate the overlay logic for backup endpoint parameters:
// ep.Params inherits the main Params when nil is used; When not nil, the entire group covers the main params.
func TestApplyFailoverEndpoint_ParamsOverride(t *testing.T) {
	main := config.LLMConfig{
		Params: config.ModelParams{Temperature: 0.7, TopP: 0.9, MaxTokens: 2048},
	}
	// No Params → Full heir to the master
	got := applyFailoverEndpoint(main, config.FailoverEndpoint{Url: "https://backup"})
	require.InDelta(t, 0.7, got.Params.Temperature, 1e-6)
	require.Equal(t, 2048, got.Params.MaxTokens)
	require.Equal(t, "https://backup", got.Url)
	// There are Params → full group coverage
	got = applyFailoverEndpoint(main, config.FailoverEndpoint{
		Url:    "https://backup",
		Params: &config.ModelParams{Temperature: 0.2, TopP: 0.5, MaxTokens: 512},
	})
	require.InDelta(t, 0.2, got.Params.Temperature, 1e-6)
	require.Equal(t, 512, got.Params.MaxTokens)
}

// TestFailoverEndpoint_ParamsJSONRoundtrip Verifying the JSON round trip of FailoverEndpoint.Params:
// Ensure the frontend ep.params(camelCase) → JSON → FailoverEndpoint.Params link fields are aligned,
// and omitempty (does not output params fields when nil).
func TestFailoverEndpoint_ParamsJSONRoundtrip(t *testing.T) {
	// Non-nil: Serialization contains params, subfield camelCase
	ep := config.FailoverEndpoint{
		Url:    "https://x",
		Key:    "k",
		Model:  "m",
		Params: &config.ModelParams{Temperature: 0.3, TopP: 0.8, MaxTokens: 1024},
	}
	b, err := json.Marshal(ep)
	require.NoError(t, err)
	require.Contains(t, string(b), `"params"`)
	require.Contains(t, string(b), `"topP":0.8`)
	require.Contains(t, string(b), `"maxTokens":1024`)
	// Deserialized round trip
	var out config.FailoverEndpoint
	require.NoError(t, json.Unmarshal(b, &out))
	require.NotNil(t, out.Params)
	require.InDelta(t, 0.3, out.Params.Temperature, 1e-6)
	require.Equal(t, 1024, out.Params.MaxTokens)

	// nil: does not output params fields (omitempty)
	noParams := config.FailoverEndpoint{Url: "https://x"}
	b2, err := json.Marshal(noParams)
	require.NoError(t, err)
	require.NotContains(t, string(b2), `"params"`)
}

// TestMap2Struct_DecodesFailoverParams Verify maps.Map2Struct can take the
// failover[].params decoded into config.FailoverEndpoint.Params (The FailoverEndpoint field is tagged with a json tag).
// This is the actual decoding path for the agent JSON configuration → component Init; Overlay it in case tag mismatches cause params to lose silence.
func TestMap2Struct_DecodesFailoverParams(t *testing.T) {
	configuration := types.Configuration{
		"failover": []interface{}{
			map[string]interface{}{
				"url": "https://backup.example.com/v1",
				"params": map[string]interface{}{
					"temperature": 0.2,
					"topP":        0.5,
					"maxTokens":   512,
				},
			},
		},
	}
	var cfg config.LLMConfig
	require.NoError(t, maps.Map2Struct(configuration, &cfg))
	require.Len(t, cfg.Failover, 1)
	require.NotNil(t, cfg.Failover[0].Params, "Map2Struct 应解码 failover[].params（json tag）")
	require.InDelta(t, 0.2, cfg.Failover[0].Params.Temperature, 1e-6)
	require.Equal(t, 512, cfg.Failover[0].Params.MaxTokens)
}

// TestFailoverEndpoint_ExtraFieldsJSON Verifying the JSON round trip of FailoverEndpoint.Params.ExtraFields:
// Extended parameters (such as reasoning_effort model-specific parameters) can be correctly serialized/deserialized and retained applyFailoverEndpoint entire group override.
func TestFailoverEndpoint_ExtraFieldsJSON(t *testing.T) {
	ep := config.FailoverEndpoint{
		Url:   "https://x",
		Model: "m",
		Params: &config.ModelParams{
			Temperature: 0.3,
			ExtraFields: map[string]any{"reasoning_effort": "high", "thinking.type": "enabled", "max_budget": 1024},
		},
	}
	b, err := json.Marshal(ep)
	require.NoError(t, err)
	require.Contains(t, string(b), `"extraFields"`)
	require.Contains(t, string(b), `"reasoning_effort":"high"`)
	var out config.FailoverEndpoint
	require.NoError(t, json.Unmarshal(b, &out))
	require.NotNil(t, out.Params)
	require.Equal(t, "high", out.Params.ExtraFields["reasoning_effort"])
	// After JSON back-and-forth, map[string]any digits become float64 (Go encoding/json standard behavior)
	require.Equal(t, float64(1024), out.Params.ExtraFields["max_budget"])

	// applyFailoverEndpoint covers the entire group, while ExtraFields are retained with Params
	main := config.LLMConfig{Params: config.ModelParams{Temperature: 0.9}}
	got := applyFailoverEndpoint(main, ep)
	require.Equal(t, "high", got.Params.ExtraFields["reasoning_effort"])
	require.InDelta(t, 0.3, got.Params.Temperature, 1e-6)
}
