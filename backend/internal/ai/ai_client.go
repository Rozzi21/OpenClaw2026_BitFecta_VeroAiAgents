package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// maxAIResponseBytes caps the AI provider response body we will decode (SEC-9).
const maxAIResponseBytes = 1 << 20 // 1 MiB

// MaxToolCallRounds limits how many tool-call round-trips we allow before
// forcing a final text response. This prevents infinite loops if the LLM keeps
// requesting tool calls.
const MaxToolCallRounds = 5

type Client struct {
	APIKey      string
	BaseURL     string
	Model       string
	Temperature float64
	HTTPClient  *http.Client
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents an OpenAI-compatible function call from the LLM.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the function name and JSON-encoded arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef is the OpenAI-compatible tool definition sent in the request.
type ToolDef struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

// FunctionSpec describes a function available to the LLM.
type FunctionSpec struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// ResponseFormat optionally requests structured output (OpenAI-compatible
// `response_format`). Callers pass a provider-specific schema object; it is
// emitted verbatim in the JSON body. Nil means default free-form text.
type ResponseFormat struct {
	Type       string                 `json:"type"`
	JsonSchema map[string]interface{} `json:"json_schema,omitempty"`
}

type CompletionRequest struct {
	Messages []Message `json:"messages"`
	Tools    []ToolDef `json:"tools,omitempty"`

	// ResponseFormat requests structured output (e.g. JSON schema) on the final
	// assistant message instead of free-form text. Used by SEC-29 for the
	// order-claim check so we do not parse natural language.
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

type CompletionResponse struct {
	Text      string                 `json:"text"`
	ToolCalls []ToolCall             `json:"tool_calls,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	RawStatus int                    `json:"raw_status"`
}

func NewClient(apiKey, baseURL, model string, temperature float64, timeout time.Duration) *Client {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	if timeout <= 0 {
		timeout = 35 * time.Second
	}

	return &Client{
		APIKey:      apiKey,
		BaseURL:     strings.TrimRight(baseURL, "/"),
		Model:       model,
		Temperature: temperature,
		HTTPClient:  &http.Client{Timeout: timeout},
	}
}

func (c *Client) Generate(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if c.APIKey == "" {
		return CompletionResponse{
			Text: "AI API key is empty; using local travel assistant fallback response.",
			Metadata: map[string]interface{}{
				"mode":  "local_fallback",
				"model": c.Model,
			},
			RawStatus: http.StatusOK,
		}, nil
	}

	payload := map[string]interface{}{
		"model":       c.Model,
		"messages":    req.Messages,
		"temperature": c.Temperature,
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer res.Body.Close()

	// SEC-9: cap how much of the provider response we will read/decode so a
	// runaway or malicious response cannot exhaust memory.
	limited := io.LimitReader(res.Body, maxAIResponseBytes)
	var raw map[string]interface{}
	if err := json.NewDecoder(limited).Decode(&raw); err != nil {
		return CompletionResponse{}, err
	}

	out := CompletionResponse{
		Text:      extractText(raw),
		ToolCalls: extractToolCalls(raw),
		Metadata:  raw,
		RawStatus: res.StatusCode,
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, fmt.Errorf("ai provider returned status %d", res.StatusCode)
	}
	if len(out.ToolCalls) == 0 && out.Text == "" {
		out.Text = "AI provider returned an empty text response."
	}
	return out, nil
}

// extractToolCalls parses tool_calls from an OpenAI-compatible response.
func extractToolCalls(raw map[string]interface{}) []ToolCall {
	choices, ok := raw["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}
	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return nil
	}
	toolCallsRaw, ok := message["tool_calls"].([]interface{})
	if !ok || len(toolCallsRaw) == 0 {
		return nil
	}

	var calls []ToolCall
	for _, tcRaw := range toolCallsRaw {
		tcMap, ok := tcRaw.(map[string]interface{})
		if !ok {
			continue
		}
		tc := ToolCall{
			ID:   getStr(tcMap, "id"),
			Type: getStr(tcMap, "type"),
		}
		if fnMap, ok := tcMap["function"].(map[string]interface{}); ok {
			tc.Function = FunctionCall{
				Name:      getStr(fnMap, "name"),
				Arguments: getStr(fnMap, "arguments"),
			}
		}
		if tc.Function.Name != "" {
			calls = append(calls, tc)
			log.Printf("[ai] tool_call: id=%s function=%s", tc.ID, tc.Function.Name)
		}
	}
	return calls
}

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// extractText extracts the final assistant text from an OpenAI-compatible
// response payload. It prefers the standard content field, then falls back to
// reasoning fields used by models such as Qwen or DeepSeek, then scans for any
// non-empty string field. This keeps the client provider-agnostic.
func extractText(raw map[string]interface{}) string {
	if choices, ok := raw["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if message, ok := choice["message"].(map[string]interface{}); ok {
				if text, field := extractString(message, "content"); text != "" {
					log.Printf("[ai] extracted text from choices[0].message.%s", field)
					return text
				}
				for _, key := range []string{"reasoning_content", "reasoning", "thinking"} {
					if text, field := extractString(message, key); text != "" {
						log.Printf("[ai] extracted text from choices[0].message.%s (content empty)", field)
						return text
					}
				}
				log.Printf("[ai] choices[0].message has no usable text field")
				return ""
			}
			if text, field := extractString(choice, "text"); text != "" {
				log.Printf("[ai] extracted text from choices[0].%s", field)
				return text
			}
		}
	}

	for _, key := range []string{"text", "output", "content", "message"} {
		if value, ok := raw[key].(string); ok && value != "" {
			log.Printf("[ai] extracted text from top-level %s", key)
			return value
		}
	}

	log.Printf("[ai] no usable text field found in response")
	return ""
}

// extractString returns a non-empty trimmed string from m[key] and the key
// name that matched. It returns ("", "") if the value is missing, not a
// string, or empty after trimming.
func extractString(m map[string]interface{}, key string) (string, string) {
	if v, ok := m[key].(string); ok {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), key
		}
	}
	return "", ""
}

// GenerateStream runs a streaming chat completion (PERF-1). The provider sends
// Server-Sent Events chunks; each `choices[0].delta.content` text fragment is
// forwarded to onDelta as soon as it arrives so the caller can flush it to the
// client and lower Time-To-First-Token. Tool-call deltas are accumulated into
// full ToolCalls and returned together with the final metadata, mirroring the
// non-streaming CompletionResponse contract.
//
// Design notes (PERF-1, 3 Agu 2026):
//   - Only the FINAL text round is streamed. Tool-call rounds still use the
//     non-streaming Generate because they need the complete tool_calls array
//     up front to dispatch via MCP. Streaming the assistant text after the
//     tool loop is where the bulk of user-perceived latency lives.
//   - The HTTP client Timeout is the overall request budget (cfg.AITimeout);
//     the request ctx (SEC-26) adds cancellation on client disconnect so
//     whichever fires first cancels the stream mid-flight.
//   - Response body is NOT capped by io.LimitReader here: a stream is read
//     incrementally and each chunk is a small JSON object, so peak memory is
//     bounded by the accumulated text length rather than one giant buffer.
//     A malicious/buggy provider could still stream forever, which is bounded
//     by the request timeout above.
func (c *Client) GenerateStream(ctx context.Context, req CompletionRequest, onDelta func(text string)) (CompletionResponse, error) {
	// Fallback path mirrors Generate: no key -> single synthetic delta so the
	// UX of the streaming handler stays identical (one delta + done event).
	if c.APIKey == "" {
		fallback := "AI API key is empty; using local travel assistant fallback response."
		if onDelta != nil {
			onDelta(fallback)
		}
		return CompletionResponse{
			Text: fallback,
			Metadata: map[string]interface{}{
				"mode":  "local_fallback",
				"model": c.Model,
			},
			RawStatus: http.StatusOK,
		}, nil
	}

	payload := map[string]interface{}{
		"model":       c.Model,
		"messages":    req.Messages,
		"temperature": c.Temperature,
		"stream":      true,
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	res, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// Non-2xx stream responses usually carry a JSON error body, not SSE.
		limited := io.LimitReader(res.Body, maxAIResponseBytes)
		var raw map[string]interface{}
		_ = json.NewDecoder(limited).Decode(&raw)
		return CompletionResponse{Metadata: raw, RawStatus: res.StatusCode}, fmt.Errorf("ai provider returned status %d", res.StatusCode)
	}

	var (
		fullText      strings.Builder
		reasoningText strings.Builder       // reasoning_content, NOT streamed to user
		toolCalls     = map[int]*ToolCall{} // accumulate deltas keyed by tool index
		metadata      = map[string]interface{}{}
		finish        string
	)

	// Use a bufio.Reader instead of bufio.Scanner for byte-level streaming.
	// bufio.Scanner waits for a newline and may batch multiple SSE lines if
	// the provider flushes them in the same TCP packet. A Reader lets us
	// parse events as soon as a blank line delimiter is available, without
	// waiting for line boundaries that may arrive together.
	reader := bufio.NewReaderSize(res.Body, 64*1024)
	for {
		if ctx.Err() != nil {
			return CompletionResponse{}, ctx.Err()
		}

		// Read until the SSE blank-line delimiter. We still block on the
		// reader (which in turn reads from the HTTP response body), so a
		// context cancellation will unblock the underlying connection close
		// and return an error.
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			// A context-cancelled read returns a wrapped error; surface it
			// only if it is not the expected cancellation path.
			if ctx.Err() != nil {
				return CompletionResponse{}, ctx.Err()
			}
			return CompletionResponse{}, err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// End of one SSE event. Continue to read the next event.
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip malformed chunks rather than aborting the whole stream.
			log.Printf("[ai] stream: skipping unparseable chunk: %v", err)
			continue
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}

		if delta, ok := choice["delta"].(map[string]interface{}); ok {
			if content, _ := delta["content"].(string); content != "" {
				fullText.WriteString(content)
				if onDelta != nil {
					onDelta(content)
				}
			}
			// Accumulate tool-call deltas (some providers stream them in pieces).
			if tcsRaw, ok := delta["tool_calls"].([]interface{}); ok {
				accumulateToolCallDeltas(toolCalls, tcsRaw)
			}
			// Reasoning models (DeepSeek/Qwen) stream reasoning_content during
			// the "thinking" phase BEFORE content arrives. Accumulate it
			// separately as a fallback (used only if the stream ends with
			// zero content, matching extractText semantics) but NEVER forward
			// it to onDelta. Previously the per-chunk `fullText.Len() == 0`
			// guard let the FIRST reasoning token ("The") leak into fullText
			// and onDelta; when real content ("Halo!") arrived it appended to
			// "The" -> "TheHalo!". Reasoning tokens must not reach the user.
			if rc, _ := delta["reasoning_content"].(string); rc != "" {
				reasoningText.WriteString(rc)
			}
		}

		if fr, _ := choice["finish_reason"].(string); fr != "" {
			finish = fr
		}

		// Keep the last chunk's id/model for metadata parity with Generate.
		if id, _ := chunk["id"].(string); id != "" {
			metadata["id"] = id
		}
		if m, _ := chunk["model"].(string); m != "" {
			metadata["model"] = m
		}
	}
	metadata["finish_reason"] = finish
	metadata["mode"] = "stream"

	out := CompletionResponse{
		Text:      fullText.String(),
		ToolCalls: finalizeToolCalls(toolCalls),
		Metadata:  metadata,
		RawStatus: res.StatusCode,
	}
	if len(out.ToolCalls) == 0 && out.Text == "" {
		// No content streamed; fall back to reasoning_content if the provider
		// sent any (pure reasoning models). It was not streamed live, so the
		// frontend handler animates it via shouldAnimate.
		if reasoningText.Len() > 0 {
			out.Text = reasoningText.String()
		} else {
			out.Text = "AI provider returned an empty text response."
		}
	}
	return out, nil
}

// accumulateToolCallDeltas merges streaming tool-call fragments into the
// running ToolCall map keyed by the tool index. OpenAI-compatible providers
// send the function name in the first delta for an index and append argument
// token fragments in subsequent deltas for the same index.
func accumulateToolCallDeltas(toolCalls map[int]*ToolCall, deltas []interface{}) {
	for _, d := range deltas {
		dMap, ok := d.(map[string]interface{})
		if !ok {
			continue
		}
		idxF, _ := dMap["index"].(float64)
		idx := int(idxF)

		tc, ok := toolCalls[idx]
		if !ok {
			tc = &ToolCall{Type: "function"}
			toolCalls[idx] = tc
		}
		if id, _ := dMap["id"].(string); id != "" {
			tc.ID = id
		}
		if t, _ := dMap["type"].(string); t != "" {
			tc.Type = t
		}
		if fnMap, ok := dMap["function"].(map[string]interface{}); ok {
			if name, _ := fnMap["name"].(string); name != "" {
				tc.Function.Name = name
			}
			if args, _ := fnMap["arguments"].(string); args != "" {
				tc.Function.Arguments += args
			}
		}
	}
}

// finalizeToolCalls returns the accumulated tool calls ordered by their stream
// index so callers see them in the order the model emitted them.
func finalizeToolCalls(toolCalls map[int]*ToolCall) []ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	indices := make([]int, 0, len(toolCalls))
	for i := range toolCalls {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	out := make([]ToolCall, 0, len(indices))
	for _, i := range indices {
		tc := toolCalls[i]
		if tc.Function.Name != "" {
			out = append(out, *tc)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
