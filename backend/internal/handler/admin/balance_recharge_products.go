package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *PaymentHandler) GetBalanceRechargeProducts(c *gin.Context) {
	catalog, err := h.configService.GetBalanceRechargeCatalog(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, catalog)
}

func (h *PaymentHandler) UpdateBalanceRechargeProducts(c *gin.Context) {
	var request struct {
		Enabled  *bool                             `json:"enabled" binding:"required"`
		Products *[]service.BalanceRechargeProduct `json:"products" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	catalog, err := h.configService.UpdateBalanceRechargeCatalog(c.Request.Context(), service.BalanceRechargeCatalog{
		Enabled: *request.Enabled, Products: *request.Products,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, catalog)
}
