package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/services"
)

// PERF-1 (3 Agu 2026): streaming chat over Server-Sent Events.
//
// When a client sends `{"stream": true}` to POST /chat (guest or authenticated),
// the assistant's final text response is streamed token-by-token instead of
// being buffered whole. Each `delta` SSE event carries a text fragment; a
// terminal `done` event carries the full ChatResult (recommendations, workflow,
// recommendation_reason) so the client can render package cards after the text
// finishes. An `error` event is emitted (then the connection closed) if the
// tool loop or stream fails mid-flight.
//
// Transport notes:
//   - We reuse the BUG-4 SSE hardening pattern (ResponseController write
//     deadline per write + Flush) so a half-open client connection is detected
//     and the goroutine exits instead of leaking. The global WriteTimeout=15s
//     is disabled dynamically for this long-lived response, exactly like the
//     /events/stream handler.
//   - The response is text/event-stream, NOT the standard JSON envelope. The
//     envelope contract (coding-rules §1.3) governs regular JSON HTTP
//     responses; SSE is a separate streaming transport where each event is a
//     small JSON object. Non-stream requests still go through utils.Success.
//   - Context propagation (SEC-26) is preserved: c.Request.Context() flows
//     into ChatStream and into the streaming HTTP call to the provider, so a
//     client disconnect cancels the upstream stream too.

// chatStreamWriteDeadline is the per-write deadline for SSE chunks. If a write
// (delta/flush) does not complete within this window the connection is treated
// as dead and the handler returns. Mirrors the 10s window used by the
// /events/stream handler (BUG-4).
const chatStreamWriteDeadline = 10 * time.Second

// streamChat runs AIService.ChatStream and forwards deltas to the client as SSE.
// It is shared by the guest and authenticated chat endpoints. `setCookie` is an
// optional callback invoked BEFORE the first byte of the body is written (so
// the guest session cookie is still set on a streaming response); nil skips it.
func (h *Handler) streamChat(c *gin.Context, chatCtx services.ChatContext, req dto.ChatRequest, setCookie func()) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	// Hint reverse proxies not to buffer the stream (common for Nginx/Caddy).
	c.Header("X-Accel-Buffering", "no")

	if setCookie != nil {
		setCookie()
	}

	rc := http.NewResponseController(c.Writer)
	// ARCH-3/BUG-4: disable the global 15s WriteTimeout for this long-lived
	// streaming response; we manage write deadlines per-write instead.
	_ = rc.SetWriteDeadline(time.Time{})

	// send writes one SSE event and flushes. Returns false if the connection
	// is dead (write/flush error) so the caller can stop streaming.
	send := func(eventType string, data interface{}) bool {
		payload, err := json.Marshal(data)
		if err != nil {
			return false
		}
		_ = rc.SetWriteDeadline(time.Now().Add(chatStreamWriteDeadline))
		_, _ = c.Writer.WriteString("event: " + eventType + "\n")
		_, _ = c.Writer.WriteString("data: " + string(payload) + "\n\n")
		if err := rc.Flush(); err != nil {
			return false
		}
		return true
	}

	// onDelta is invoked inline by the AI client while scanning the upstream
	// SSE stream. We forward each fragment immediately so TTFT stays low.
	onDelta := func(text string) {
		// If the client already disconnected, the ctx will be cancelled and
		// GenerateStream will return shortly; ignore further write attempts.
		if !send("delta", map[string]string{"content": text}) {
			return
		}
	}

	result, err := h.Services.AI.ChatStream(c.Request.Context(), chatCtx, req, onDelta)
	if err != nil {
		// Try to surface the error to the client; if the connection is already
		// dead the send is a no-op and we just return.
		_ = send("error", map[string]string{"message": "Maaf, Vero belum bisa memproses permintaan ini."})
		return
	}

	// Terminal event carries the full result (minus SessionID, which is
	// json:"-"). The client uses `done` to finalize the message (packages,
	// recommendation flags, workflow) exactly like the non-stream response.
	_ = send("done", result)
}
