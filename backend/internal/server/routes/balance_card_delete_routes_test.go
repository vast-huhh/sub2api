package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBalanceCardDeleteRoutesRequireAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{BalanceCard: adminhandler.NewBalanceCardHandler(nil)}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.AbortWithStatus(http.StatusForbidden)
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)
	for _, path := range []string{"/api/v1/admin/balance-card-plans/1", "/api/v1/admin/balance-cards/1"} {
		for _, auth := range []string{"", "Bearer user-token"} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodDelete, path, nil)
			request.Header.Set("Authorization", auth)
			router.ServeHTTP(recorder, request)
			want := http.StatusUnauthorized
			if auth != "" {
				want = http.StatusForbidden
			}
			require.Equal(t, want, recorder.Code, path)
		}
	}
}
