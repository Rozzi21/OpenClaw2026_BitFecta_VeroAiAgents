package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/dto"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/utils"
)

func (h *Handler) Logs(c *gin.Context) {
	var query dto.ListQuery
	_ = c.ShouldBindQuery(&query)
	query.Normalize()
	logs, err := h.Services.Logs.Logs(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "AI logs", logs)
}

func (h *Handler) WorkflowLogs(c *gin.Context) {
	h.Logs(c)
}

func (h *Handler) ToolCalls(c *gin.Context) {
	var query dto.ListQuery
	_ = c.ShouldBindQuery(&query)
	query.Normalize()
	calls, err := h.Services.Logs.ToolCalls(query)
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Tool calls", calls)
}

func (h *Handler) Analytics(c *gin.Context) {
	data, err := h.Services.Analytics.Dashboard()
	if err != nil {
		utils.ServerError(c, err)
		return
	}
	utils.Success(c, http.StatusOK, "Analytics dashboard", data)
}
