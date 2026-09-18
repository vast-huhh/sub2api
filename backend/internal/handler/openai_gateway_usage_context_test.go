package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSubmitUsageRecordTaskCopiesRequestContext(t *testing.T) {
	parent := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-request-123")
	parent = context.WithValue(parent, ctxkey.RequestID, "request-456")

	var gotClientRequestID string
	var gotRequestID string
	h := &GatewayHandler{}
	h.submitUsageRecordTask(parent, func(ctx context.Context) {
		gotClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		gotRequestID, _ = ctx.Value(ctxkey.RequestID).(string)
	})

	require.Equal(t, "client-request-123", gotClientRequestID)
	require.Equal(t, "request-456", gotRequestID)
}

func TestOpenAISubmitUsageRecordTaskCopiesRequestContext(t *testing.T) {
	parent := context.WithValue(context.Background(), ctxkey.ClientRequestID, "openai-client-request-123")
	parent = context.WithValue(parent, ctxkey.RequestID, "openai-request-456")

	var gotClientRequestID string
	var gotRequestID string
	h := &OpenAIGatewayHandler{}
	h.submitUsageRecordTask(parent, func(ctx context.Context) {
		gotClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		gotRequestID, _ = ctx.Value(ctxkey.RequestID).(string)
	})

	require.Equal(t, "openai-client-request-123", gotClientRequestID)
	require.Equal(t, "openai-request-456", gotRequestID)
}

func TestUsageRecordTaskPreservesCodexTurnStateLength(t *testing.T) {
	for _, length := range []int{0, 16384} {
		state := strings.Repeat("x", length)
		parent, cancel := context.WithCancel(context.WithValue(context.WithValue(context.Background(), ctxkey.CodexTurnStateLength, length), ctxkey.CodexTurnState, state))
		cancel()
		for _, submit := range []func(context.Context, service.UsageRecordTask){
			(&GatewayHandler{}).submitUsageRecordTask,
			(&OpenAIGatewayHandler{}).submitUsageRecordTask,
		} {
			submit(parent, func(ctx context.Context) {
				require.NoError(t, ctx.Err())
				require.Equal(t, length, ctx.Value(ctxkey.CodexTurnStateLength))
				require.Equal(t, state, ctx.Value(ctxkey.CodexTurnState))
			})
		}
	}
}
