//go:build unit

package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type authBalanceCardRepo struct {
	service.BalanceCardRepository
	snapshot *service.BalanceCardWalletSnapshot
	err      error
	calls    int
}

func (r *authBalanceCardRepo) GetWalletSnapshot(_ context.Context, userID int64, _ time.Time) (*service.BalanceCardWalletSnapshot, error) {
	r.calls++
	if userID != 10 {
		return nil, errors.New("unexpected wallet owner")
	}
	return r.snapshot, r.err
}

// Exercise the production provider, both auth formats, and the real billing
// preflight together. A usable card must not require a cash-cache lookup.
func TestAPIKeyAuthBalanceCardWithExhaustedCash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, google := range []bool{false, true} {
		name := "standard"
		if google {
			name = "google"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name                                            string
				balance                                         float64
				mutate                                          func(*service.BalanceCardWalletSnapshot)
				noCard, lookupError, disabledUser, exhaustedKey bool
				status                                          int
			}{
				{name: "zero cash with quota", status: 200},
				{name: "negative cash with quota", balance: -1, status: 200},
				{name: "fallback off with quota", mutate: func(c *service.BalanceCardWalletSnapshot) { c.FallbackEnabled = false }, status: 200},
				{name: "daily auto reset", mutate: func(c *service.BalanceCardWalletSnapshot) { c.DailyUsageUSD = 10; c.AutoResetEnabled = true }, status: 200},
				{name: "weekly auto reset", mutate: func(c *service.BalanceCardWalletSnapshot) { c.WeeklyUsageUSD = 50; c.AutoResetEnabled = true }, status: 200},
				{name: "no card", noCard: true, status: 403},
				{name: "expired", mutate: func(c *service.BalanceCardWalletSnapshot) { c.ExpiresAt = timezone.Now().Add(-time.Hour) }, status: 403},
				{name: "daily exhausted", mutate: func(c *service.BalanceCardWalletSnapshot) { c.DailyUsageUSD = 10 }, status: 403},
				{name: "weekly exhausted", mutate: func(c *service.BalanceCardWalletSnapshot) { c.WeeklyUsageUSD = 50 }, status: 403},
				{name: "total exhausted despite auto reset", mutate: func(c *service.BalanceCardWalletSnapshot) { c.MonthlyUsageUSD = 200; c.AutoResetEnabled = true }, status: 403},
				{name: "reset limit", mutate: func(c *service.BalanceCardWalletSnapshot) {
					c.DailyUsageUSD = 10
					c.AutoResetEnabled = true
					c.ResetCount = 20
				}, status: 403},
				{name: "insufficient reset term", mutate: func(c *service.BalanceCardWalletSnapshot) {
					c.DailyUsageUSD = 10
					c.AutoResetEnabled = true
					c.ExpiresAt = timezone.Now().Add(time.Hour)
				}, status: 403},
				{name: "wallet unavailable", lookupError: true, status: 503},
				{name: "card cannot bypass disabled user", disabledUser: true, status: 401},
				{name: "card cannot bypass key quota", exhaustedKey: true, status: 429},
			} {
				t.Run(tc.name, func(t *testing.T) {
					now := timezone.Now()
					card := &service.BalanceCardWalletSnapshot{CardID: 1, UserID: 10, CardType: "month", ValidityDays: 30,
						ExpiresAt: now.Add(30 * 24 * time.Hour), DailyWindowStart: &now, WeeklyWindowStart: &now,
						DailyQuotaUSD: 10, WeeklyQuotaUSD: 50, MonthlyQuotaUSD: 200, MaxResetCount: 20, FallbackEnabled: true}
					if tc.mutate != nil {
						tc.mutate(card)
					}
					cardRepo := &authBalanceCardRepo{snapshot: card}
					if tc.noCard {
						cardRepo.snapshot = nil
					}
					if tc.lookupError {
						cardRepo.err = errors.New("database unavailable")
					}
					cfg := &config.Config{RunMode: config.RunModeStandard}
					cfg.Billing.MinimumBalanceReserve = 1
					billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
					t.Cleanup(billing.Stop)
					billing.SetBalanceCardDeps(cardRepo, nil)
					user := &service.User{ID: 10, Status: service.StatusActive, Role: service.RoleUser, Balance: tc.balance}
					if tc.disabledUser {
						user.Status = "disabled"
					}
					key := &service.APIKey{ID: 101, UserID: 10, Key: "balance-card-auth-test", Status: service.StatusActive, User: user}
					if tc.exhaustedKey {
						key.Quota = 1
						key.QuotaUsed = 1
					}
					keyRepo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { return key, nil }}
					apiKeyService := service.ProvideAPIKeyService(keyRepo, nil, nil, nil, nil, nil, cfg, billing, nil)
					r := gin.New()
					if google {
						r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
					} else {
						r.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
					}
					reached := false
					r.POST("/t", func(c *gin.Context) {
						reached = true
						require.NoError(t, billing.CheckBillingEligibility(c.Request.Context(), user, key, nil, nil, ""))
						c.Status(http.StatusOK)
					})
					w := httptest.NewRecorder()
					req := httptest.NewRequest(http.MethodPost, "/t", nil)
					req.Header.Set("x-api-key", key.Key)
					r.ServeHTTP(w, req)
					require.Equal(t, tc.status, w.Code, w.Body.String())
					require.Equal(t, tc.status == http.StatusOK, reached)
					if tc.disabledUser || tc.exhaustedKey {
						require.Zero(t, cardRepo.calls)
					}
				})
			}
		})
	}
}
