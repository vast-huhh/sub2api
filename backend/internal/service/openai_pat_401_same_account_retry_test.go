package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIPAT401SameAccountRetryPolicy(t *testing.T) {
	ctx := context.Background()

	newPAT := func(id int64) *Account {
		return &Account{
			ID:       id,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				"auth_mode": OpenAIAuthModePersonalAccessToken,
			},
		}
	}

	t.Run("direct PAT retries same account once even outside pool mode", func(t *testing.T) {
		svc := &OpenAIGatewayService{}

		retryable, limit := svc.openAIUpstreamSameAccountRetryPolicy(ctx, newPAT(101), http.StatusUnauthorized, false)

		require.True(t, retryable)
		require.Equal(t, 1, limit)
	})

	t.Run("shadow account follows credential owner PAT policy", func(t *testing.T) {
		parentID := int64(201)
		shadow := &Account{
			ID:              202,
			ParentAccountID: &parentID,
			Platform:        PlatformOpenAI,
			Type:            AccountTypeOAuth,
			Status:          StatusActive,
		}
		svc := &OpenAIGatewayService{accountRepo: newStubCredRepo(newPAT(parentID))}

		retryable, limit := svc.openAIUpstreamSameAccountRetryPolicy(ctx, shadow, http.StatusUnauthorized, false)

		require.True(t, retryable)
		require.Equal(t, 1, limit)
	})

	t.Run("pool mode non PAT keeps account configured retry policy", func(t *testing.T) {
		account := &Account{
			ID:       301,
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Status:   StatusActive,
			Credentials: map[string]any{
				"pool_mode": true,
			},
		}
		svc := &OpenAIGatewayService{}

		retryable, limit := svc.openAIUpstreamSameAccountRetryPolicy(ctx, account, http.StatusUnauthorized, true)

		require.True(t, retryable)
		require.Zero(t, limit)
	})

	t.Run("non pool non PAT does not force same account retry", func(t *testing.T) {
		account := &Account{
			ID:       401,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
		}
		svc := &OpenAIGatewayService{}

		retryable, limit := svc.openAIUpstreamSameAccountRetryPolicy(ctx, account, http.StatusUnauthorized, false)

		require.False(t, retryable)
		require.Zero(t, limit)
	})
}
