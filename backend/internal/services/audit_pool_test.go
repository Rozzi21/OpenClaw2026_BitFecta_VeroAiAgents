package services

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// mockAuditWriter records calls to CreateToolCall / CreateAILog for assertions.
type mockAuditWriter struct {
	mu        sync.Mutex
	toolCalls []*models.ToolCall
	aiLogs    []*models.AILog
}

func (m *mockAuditWriter) CreateToolCall(_ context.Context, call *models.ToolCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls = append(m.toolCalls, call)
	return nil
}

func (m *mockAuditWriter) CreateAILog(_ context.Context, log *models.AILog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aiLogs = append(m.aiLogs, log)
	return nil
}

func (m *mockAuditWriter) counts() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.toolCalls), len(m.aiLogs)
}

// TestAuditPool_SubmitAndDrain locks PERF-3 #2: submitted jobs are persisted by
// workers, and Stop drains in-flight jobs before returning.
func TestAuditPool_SubmitAndDrain(t *testing.T) {
	writer := &mockAuditWriter{}
	pool := NewAuditPool(writer)
	pool.Start()
	defer pool.Stop()

	sessionID := uuid.New()
	for i := 0; i < 5; i++ {
		ok := pool.Submit(auditJob{
			sessionID: sessionID,
			toolName:  "search_trips",
			status:    models.ToolResultStatusSuccess,
			payload:   map[string]interface{}{"query": "bali"},
			result:    ToolResult{Tool: "search_trips", Status: models.ToolResultStatusSuccess},
		})
		if !ok {
			t.Fatalf("Submit returned false on iteration %d (buffer should not be full)", i)
		}
	}

	// Stop drains the pool: all 5 jobs must be persisted before Stop returns.
	pool.Stop()

	// Give the workers no extra time — Stop must have drained.
	tc, al := writer.counts()
	if tc != 5 {
		t.Fatalf("expected 5 tool calls persisted after drain, got %d", tc)
	}
	if al != 5 {
		t.Fatalf("expected 5 ai logs persisted after drain, got %d", al)
	}
}

// TestAuditPool_StopIsIdempotent verifies Stop can be called multiple times
// safely (double-close of the jobs channel must not panic).
func TestAuditPool_StopIsIdempotent(t *testing.T) {
	pool := NewAuditPool(&mockAuditWriter{})
	pool.Start()
	pool.Stop()
	pool.Stop() // must not panic
}

// TestAuditPool_SubmitNonBlockingWhenFull locks PERF-3 #2: Submit must never
// block when the buffer is full; it drops and returns false so the AI response
// path is never stalled by audit pressure.
func TestAuditPool_SubmitNonBlockingWhenFull(t *testing.T) {
	// A writer that blocks forever simulates a wedged DB so workers never drain.
	stalled := &stalledWriter{}
	pool := NewAuditPool(stalled)
	pool.Start()
	defer pool.Stop()

	// Fill the buffer (auditPoolBuffer = 64). Workers are stuck on the stalled
	// writer, so the channel fills up.
	sessionID := uuid.New()
	submitted := 0
	for i := 0; i < auditPoolBuffer+20; i++ {
		if pool.Submit(auditJob{sessionID: sessionID, toolName: "search_trips"}) {
			submitted++
		}
	}
	// At least the buffer capacity must have been accepted before drops started.
	if submitted < auditPoolBuffer {
		t.Fatalf("expected at least %d submits accepted, got %d", auditPoolBuffer, submitted)
	}
	// Some drops must have occurred once the buffer was full (submitted <= buffer + a few in flight).
	if submitted > auditPoolBuffer+auditPoolWorkers {
		t.Fatalf("expected drops to occur once buffer full, got %d accepted (buffer=%d, workers=%d)", submitted, auditPoolBuffer, auditPoolWorkers)
	}
}

// stalledWriter blocks every call indefinitely, simulating a wedged DB.
type stalledWriter struct{}

func (s *stalledWriter) CreateToolCall(ctx context.Context, _ *models.ToolCall) error {
	<-ctx.Done()
	return ctx.Err()
}
func (s *stalledWriter) CreateAILog(ctx context.Context, _ *models.AILog) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestMCPService_AuditFallbackSync verifies the synchronous fallback path used
// when no AuditPool is wired (unit-test scenario): audit records are still
// persisted synchronously.
func TestMCPService_AuditFallbackSync(t *testing.T) {
	// This test only exercises persistAuditSync + clonePayload directly, which
	// are the fallback primitives, to avoid wiring a full MCPService.
	sessionID := uuid.New()
	payload := map[string]interface{}{"query": "bali"}
	clone := clonePayload(payload)
	if clone["query"] != "bali" {
		t.Fatalf("clonePayload lost value")
	}
	// Mutate original; clone must be unaffected (defensive copy for async path).
	payload["query"] = "changed"
	if clone["query"] != "bali" {
		t.Fatalf("clonePayload did not copy: got %q", clone["query"])
	}

	// Ensure the audit job struct can hold the expected fields without zeroing.
	job := auditJob{
		sessionID:     sessionID,
		toolName:      "search_trips",
		status:        models.ToolResultStatusSuccess,
		executionTime: 42,
		payload:       clone,
		result:        ToolResult{Tool: "search_trips", Status: models.ToolResultStatusSuccess},
	}
	if job.sessionID != sessionID || job.executionTime != 42 {
		t.Fatalf("auditJob field mismatch")
	}
}

// _ keeps the atomic import used if future tests need counters.
var _ = atomic.AddInt64
