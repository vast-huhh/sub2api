package admin

import (
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type BalanceCardHandler struct {
	service *service.BalanceCardService
}

func NewBalanceCardHandler(svc *service.BalanceCardService) *BalanceCardHandler {
	return &BalanceCardHandler{service: svc}
}

type balanceCardPlanRequest struct {
	Name             string  `json:"name" binding:"required,max=100"`
	Description      string  `json:"description"`
	CardType         string  `json:"card_type" binding:"required"`
	ValidityDays     int     `json:"validity_days" binding:"required,min=1,max=36500"`
	DailyQuotaUSD    float64 `json:"daily_quota_usd" binding:"gte=0"`
	WeeklyQuotaUSD   float64 `json:"weekly_quota_usd" binding:"gte=0"`
	MonthlyQuotaUSD  float64 `json:"monthly_quota_usd" binding:"gte=0"`
	FallbackDefault  *bool   `json:"fallback_default"`
	AutoResetDefault *bool   `json:"auto_reset_default"`
	MaxResetCount    *int    `json:"max_reset_count"`
	Status           string  `json:"status"`
	SortOrder        int     `json:"sort_order"`
}

func (r balanceCardPlanRequest) serviceInput() *service.CreateBalanceCardPlanInput {
	fallback := true
	if r.FallbackDefault != nil {
		fallback = *r.FallbackDefault
	}
	autoReset := false
	if r.AutoResetDefault != nil {
		autoReset = *r.AutoResetDefault
	}
	maxReset := 20
	if r.MaxResetCount != nil {
		maxReset = *r.MaxResetCount
	}
	return &service.CreateBalanceCardPlanInput{
		Name: r.Name, Description: r.Description, CardType: r.CardType,
		ValidityDays: r.ValidityDays, DailyQuotaUSD: r.DailyQuotaUSD,
		WeeklyQuotaUSD: r.WeeklyQuotaUSD, MonthlyQuotaUSD: r.MonthlyQuotaUSD,
		FallbackDefault: fallback, AutoResetDefault: autoReset,
		MaxResetCount: maxReset, Status: r.Status, SortOrder: r.SortOrder,
	}
}

func adminBalanceCardID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid balance card ID")
		return 0, false
	}
	return id, true
}

func (h *BalanceCardHandler) ListPlans(c *gin.Context) {
	plans, err := h.service.ListPlans(c.Request.Context(), true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, plans)
}

func (h *BalanceCardHandler) CreatePlan(c *gin.Context) {
	var req balanceCardPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	plan, err := h.service.CreatePlan(c.Request.Context(), req.serviceInput())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, plan)
}

func (h *BalanceCardHandler) UpdatePlan(c *gin.Context) {
	id, ok := adminBalanceCardID(c)
	if !ok {
		return
	}
	var req balanceCardPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	plan, err := h.service.UpdatePlan(c.Request.Context(), id, req.serviceInput())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, plan)
}

func (h *BalanceCardHandler) ListCards(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	filter := service.BalanceCardListFilter{Status: strings.TrimSpace(c.Query("status"))}
	if value := strings.TrimSpace(c.Query("user_id")); value != "" {
		if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
			filter.UserID = &id
		}
	}
	if value := strings.TrimSpace(c.Query("plan_id")); value != "" {
		if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
			filter.PlanID = &id
		}
	}
	items, pag, err := h.service.ListCards(c.Request.Context(), page, pageSize, filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.PaginatedWithResult(c, items, toResponsePagination(pag))
}

type assignBalanceCardRequest struct {
	UserID int64  `json:"user_id" binding:"required"`
	PlanID int64  `json:"plan_id" binding:"required"`
	Notes  string `json:"notes"`
}

func (h *BalanceCardHandler) Assign(c *gin.Context) {
	var req assignBalanceCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	card, err := h.service.Assign(c.Request.Context(), &service.AssignBalanceCardInput{
		UserID: req.UserID, PlanID: req.PlanID, AssignedBy: getAdminIDFromContext(c), Notes: req.Notes,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, card)
}

type bulkAssignBalanceCardRequest struct {
	UserIDs []int64 `json:"user_ids" binding:"required,min=1,max=1000"`
	PlanID  int64   `json:"plan_id" binding:"required"`
	Notes   string  `json:"notes"`
}

func (h *BalanceCardHandler) BulkAssign(c *gin.Context) {
	var req bulkAssignBalanceCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	cards, err := h.service.BulkAssign(c.Request.Context(), req.UserIDs, req.PlanID, getAdminIDFromContext(c), req.Notes)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cards)
}

type adjustBalanceCardRequest struct {
	Days int `json:"days" binding:"required,min=-36500,max=36500"`
}

type adminResetBalanceCardRequest struct {
	OperationKey string `json:"operation_key"`
}

func (h *BalanceCardHandler) Extend(c *gin.Context) {
	id, ok := adminBalanceCardID(c)
	if !ok {
		return
	}
	var req adjustBalanceCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	card, err := h.service.Extend(c.Request.Context(), id, req.Days, getAdminIDFromContext(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, card)
}

func (h *BalanceCardHandler) ResetDaily(c *gin.Context) {
	id, ok := adminBalanceCardID(c)
	if !ok {
		return
	}
	var req adminResetBalanceCardRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if strings.TrimSpace(req.OperationKey) == "" {
		req.OperationKey = strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	}
	card, err := h.service.AdminResetDaily(c.Request.Context(), id, getAdminIDFromContext(c), req.OperationKey)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, card)
}

func (h *BalanceCardHandler) Revoke(c *gin.Context) {
	id, ok := adminBalanceCardID(c)
	if !ok {
		return
	}
	card, err := h.service.Revoke(c.Request.Context(), id, getAdminIDFromContext(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, card)
}

func (h *BalanceCardHandler) Delete(c *gin.Context) {
	id, ok := adminBalanceCardID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), id, getAdminIDFromContext(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Balance card deleted successfully"})
}

func (h *BalanceCardHandler) Ledger(c *gin.Context) {
	id, ok := adminBalanceCardID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, pag, err := h.service.ListLedger(c.Request.Context(), id, 0, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.PaginatedWithResult(c, items, toResponsePagination(pag))
}
