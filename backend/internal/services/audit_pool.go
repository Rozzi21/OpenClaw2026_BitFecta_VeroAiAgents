package services

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/repositories"
)

// AuditWriter is the narrow persistence contract the audit worker pool uses to
// write tool-call + AI-log audit records (PERF-3 #2). *repositories.Repository
// satisfies it implicitly (SEC-27 structural typing).
type AuditWriter interface {
	CreateToolCall(ctx context.Context, call *models.ToolCall) error
	CreateAILog(ctx context.Context, log *models.AILog) error
}

// Compile-time assertion that the concrete repository satisfies AuditWriter.
var _ AuditWriter = (*repositories.Repository)(nil)

// auditJob carries everything needed to persist the audit trail for one MCP
// tool execution. Marshal + DB write happen inside the worker, off the
// synchronous LLM response path.
type auditJob struct {
	sessionID     uuid.UUID
	toolName      string
	status        string
	executionTime int64
	payload       map[string]interface{}
	result        ToolResult
}

const (
	// auditPoolWorkers bounds concurrent DB writers. Low count on purpose: audit
	// is best-effort and must not starve the connection pool used by the main
	// request path (SEC-21 flood note).
	auditPoolWorkers = 2
	// auditPoolBuffer caps in-flight audit jobs. When full, Submit drops (audit
	// never blocks the AI response).
	auditPoolBuffer = 64
	// auditWriteTimeout bounds each DB write so a wedged DB cannot stall a
	// worker forever (SEC-26 detached context).
	auditWriteTimeout = 10 * time.Second
	// auditDrainTimeout bounds graceful shutdown so a wedged DB cannot hang
	// process exit. Leftover jobs are dropped (audit is best-effort).
	auditDrainTimeout = 10 * time.Second
)

// AuditPool is a bounded worker pool that persists MCP audit records (tool
// calls + AI logs) asynchronously, detached from the synchronous LLM response
// path (PERF-3 #2). Bounding the worker count + channel buffer prevents the
// goroutine/DB-connection flood that an unbounded `go func()` per call would
// risk on high tool-call volume (SEC-21 note).
//
// Submit is non-blocking: if the buffer is full the job is dropped and logged,
// so audit pressure never stalls the AI response. Workers use a detached
// context (context.Background + timeout) because audit writes outlive the HTTP
// request that produced them (SEC-26).
type AuditPool struct {
	writer   AuditWriter
	jobs     chan auditJob
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewAuditPool constructs an audit pool. Workers are not started until Start is
// called.
func NewAuditPool(writer AuditWriter) *AuditPool {
	return &AuditPool{
		writer: writer,
		jobs:   make(chan auditJob, auditPoolBuffer),
	}
}

// Start spawns the worker goroutines. Safe to call once.
func (p *AuditPool) Start() {
	for i := 0; i < auditPoolWorkers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
}

func (p *AuditPool) worker() {
	defer p.wg.Done()
	for job := range p.jobs {
		ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
		p.persist(ctx, job)
		cancel()
	}
}

func (p *AuditPool) persist(ctx context.Context, job auditJob) {
	payloadJSON, _ := json.Marshal(job.payload)
	resultJSON, _ := json.Marshal(job.result)

	toolCall := models.ToolCall{
		SessionID: job.sessionID,
		ToolName:  job.toolName,
		Payload:   string(payloadJSON),
		Result:    string(resultJSON),
		Status:    job.status,
	}
	sessionID := job.sessionID // take address of a local copy, not the loop var
	aiLog := models.AILog{
		SessionID:     &sessionID,
		Workflow:      "mcp_tool_execution",
		ToolName:      job.toolName,
		Status:        job.status,
		ExecutionTime: job.executionTime,
		Response:      string(resultJSON),
	}

	if err := p.writer.CreateToolCall(ctx, &toolCall); err != nil {
		auth.LogSecurity("tool_call_persist_failed", map[string]any{
			"session_id": job.sessionID.String(),
			"tool_name":  job.toolName,
			"error":      err.Error(),
		})
	}
	if err := p.writer.CreateAILog(ctx, &aiLog); err != nil {
		auth.LogSecurity("ai_log_persist_failed", map[string]any{
			"session_id": job.sessionID.String(),
			"workflow":   "mcp_tool_execution",
			"tool_name":  job.toolName,
			"error":      err.Error(),
		})
	}
}

// Submit enqueues an audit job. Non-blocking: returns false (and logs) when the
// buffer is full so the AI response path is never blocked by audit pressure.
func (p *AuditPool) Submit(job auditJob) bool {
	select {
	case p.jobs <- job:
		return true
	default:
		log.Printf("[audit-pool] buffer full, dropping tool_call session=%s tool=%s", job.sessionID, job.toolName)
		return false
	}
}

// Stop drains the pool: closes the jobs channel and waits for in-flight workers
// to finish, bounded by auditDrainTimeout. Safe to call multiple times. Call
// during graceful shutdown so in-flight audit records are persisted before the
// process exits.
func (p *AuditPool) Stop() {
	p.stopOnce.Do(func() {
		close(p.jobs)
	})
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(auditDrainTimeout):
		log.Printf("[audit-pool] drain timeout reached, %d job(s) may be un-persisted", len(p.jobs))
	}
}
