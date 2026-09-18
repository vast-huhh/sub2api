package middleware

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
)

// UsageRequestHeaders captures inbound metadata before forwarding can change it.
// For WebSocket requests this measures the client handshake header, shared by
// all usage records on that connection. The complete value is kept for admin copying.
func UsageRequestHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		state := c.GetHeader("X-Codex-Turn-State")
		length := len(state)
		ctx := context.WithValue(c.Request.Context(), ctxkey.CodexTurnStateLength, length)
		ctx = context.WithValue(ctx, ctxkey.CodexTurnState, state)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
