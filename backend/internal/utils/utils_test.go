package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestContextHandler_Handle(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&buf, nil)
	handler := NewContextHandler(baseHandler)
	logger := slog.New(handler)

	t.Run("with request_id in context", func(t *testing.T) {
		buf.Reset()
		ctx := context.WithValue(context.Background(), "request_id", "req-12345")
		logger.InfoContext(ctx, "test log with context")

		var logMap map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &logMap); err != nil {
			t.Fatalf("failed to unmarshal log output: %v", err)
		}

		if logMap["request_id"] != "req-12345" {
			t.Errorf("expected request_id 'req-12345', got '%v'", logMap["request_id"])
		}
		if logMap["msg"] != "test log with context" {
			t.Errorf("expected msg 'test log with context', got '%v'", logMap["msg"])
		}
	})

	t.Run("without request_id in context", func(t *testing.T) {
		buf.Reset()
		ctx := context.Background()
		logger.InfoContext(ctx, "test log without request id")

		var logMap map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &logMap); err != nil {
			t.Fatalf("failed to unmarshal log output: %v", err)
		}

		if _, exists := logMap["request_id"]; exists {
			t.Errorf("expected request_id to not exist in log, got '%v'", logMap["request_id"])
		}
	})
}

func TestAPIResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		action         func(c *gin.Context)
		expectedStatus int
		expectedResp   APIResponse
	}{
		{
			name: "Success",
			action: func(c *gin.Context) {
				Success(c, http.StatusOK, "success message", gin.H{"key": "value"})
			},
			expectedStatus: http.StatusOK,
			expectedResp: APIResponse{
				Success: true,
				Message: "success message",
				Data:    map[string]interface{}{"key": "value"},
			},
		},
		{
			name: "BadRequest",
			action: func(c *gin.Context) {
				BadRequest(c, "bad request message", "invalid field")
			},
			expectedStatus: http.StatusBadRequest,
			expectedResp: APIResponse{
				Success: false,
				Message: "bad request message",
				Error:   "invalid field",
			},
		},
		{
			name: "Unauthorized",
			action: func(c *gin.Context) {
				Unauthorized(c, "unauthorized message")
			},
			expectedStatus: http.StatusUnauthorized,
			expectedResp: APIResponse{
				Success: false,
				Message: "unauthorized message",
				Error:   map[string]interface{}{},
			},
		},
		{
			name: "Forbidden",
			action: func(c *gin.Context) {
				Forbidden(c, "forbidden message")
			},
			expectedStatus: http.StatusForbidden,
			expectedResp: APIResponse{
				Success: false,
				Message: "forbidden message",
				Error:   map[string]interface{}{},
			},
		},
		{
			name: "NotFound",
			action: func(c *gin.Context) {
				NotFound(c, "not found message")
			},
			expectedStatus: http.StatusNotFound,
			expectedResp: APIResponse{
				Success: false,
				Message: "not found message",
				Error:   map[string]interface{}{},
			},
		},
		{
			name: "ServerError",
			action: func(c *gin.Context) {
				c.Set("request_id", "req-err-999")
				ServerError(c, errors.New("database connection failed"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedResp: APIResponse{
				Success: false,
				Message: "Internal server error",
				Error:   map[string]interface{}{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req, _ := http.NewRequest(http.MethodGet, "/test", nil)
			c.Request = req

			tt.action(c)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			var resp APIResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to unmarshal response body: %v", err)
			}

			if resp.Success != tt.expectedResp.Success {
				t.Errorf("expected Success %v, got %v", tt.expectedResp.Success, resp.Success)
			}
			if resp.Message != tt.expectedResp.Message {
				t.Errorf("expected Message %q, got %q", tt.expectedResp.Message, resp.Message)
			}
		})
	}
}
