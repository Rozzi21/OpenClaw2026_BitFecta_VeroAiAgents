package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	Messages []Message        `json:"messages"`
	Tools    []ToolDef        `json:"tools,omitempty"`

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
