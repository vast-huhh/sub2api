package handler

import (
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type BalanceCardHandler struct {
	service *service.BalanceCardService
}

func NewBalanceCardHandler(svc *service.BalanceCardService) *BalanceCardHandler {
	return &BalanceCardHandler{service: svc}
}

func balanceCardSubject(c *gin.Context) (int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not found in context")
		return 0, false
	}
	return subject.UserID, true
}

func parseBalanceCardID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid balance card ID")
		return 0, false
	}
	return id, true
}

func (h *BalanceCardHandler) List(c *gin.Context) {
	userID, ok := balanceCardSubject(c)
	if !ok {
		return
	}
	cards, err := h.service.ListMine(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cards)
}

func (h *BalanceCardHandler) UpdatePreferences(c *gin.Context) {
	userID, ok := balanceCardSubject(c)
	if !ok {
		return
	}
	id, ok := parseBalanceCardID(c)
	if !ok {
		return
	}
	var req service.BalanceCardPreferencesInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	card, err := h.service.UpdateMyPreferences(c.Request.Context(), id, userID, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, card)
}

type resetBalanceCardRequest struct {
	OperationKey string `json:"operation_key"`
}

func (h *BalanceCardHandler) ResetDaily(c *gin.Context) {
	userID, ok := balanceCardSubject(c)
	if !ok {
		return
	}
	id, ok := parseBalanceCardID(c)
	if !ok {
		return
	}
	var req resetBalanceCardRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if strings.TrimSpace(req.OperationKey) == "" {
		req.OperationKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	card, err := h.service.ResetMyDaily(c.Request.Context(), id, userID, req.OperationKey)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, card)
}

func (h *BalanceCardHandler) Ledger(c *gin.Context) {
	userID, ok := balanceCardSubject(c)
	if !ok {
		return
	}
	id, ok := parseBalanceCardID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, pag, err := h.service.ListLedger(c.Request.Context(), id, userID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.PaginatedWithResult(c, items, &response.PaginationResult{
		Total: pag.Total, Page: pag.Page, PageSize: pag.PageSize, Pages: pag.Pages,
	})
}
