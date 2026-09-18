package middleware

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageRequestHeadersCapturesInboundBytes(t *testing.T) {
	for _, value := range []string{"", "opaque-blob", "é", strings.Repeat("x", 65536)} {
		r := gin.New()
		r.Use(UsageRequestHeaders())
		var captured context.Context
		r.POST("/responses", func(c *gin.Context) {
			c.Request.Header.Set("X-Codex-Turn-State", "rewritten")
			captured = c.Request.Context()
			c.Status(200)
		})
		req := httptest.NewRequest("POST", "/responses", nil)
		if value != "" {
			req.Header.Set("x-codex-turn-state", value)
		}
		r.ServeHTTP(httptest.NewRecorder(), req)
		require.Equal(t, len(value), captured.Value(ctxkey.CodexTurnStateLength))
		require.Equal(t, value, captured.Value(ctxkey.CodexTurnState))
	}
}
