package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type handlerInflightCardRepo struct {
	service.BalanceCardRepository
	card *service.BalanceCardWalletSnapshot
	err  error
}

func (r *handlerInflightCardRepo) GetWalletSnapshot(context.Context, int64, time.Time) (*service.BalanceCardWalletSnapshot, error) {
	return r.card, r.err
}

func TestReserveInflightBalance_CardCompatibility(t *testing.T) {
	for _, cash := range []float64{0, -5, 1.5} {
		for _, state := range []string{"funded", "missing", "exhausted", "lookup_error"} {
			for _, priced := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/cash=%g/priced=%t", state, cash, priced), func(t *testing.T) {
					cache := newHandlerInflightCache(cash)
					cfg := &config.Config{}
					cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60, FailClosedOnUnpriced: true}
					billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
					t.Cleanup(billing.Stop)
					now := time.Now()
					repo := &handlerInflightCardRepo{card: &service.BalanceCardWalletSnapshot{
						ExpiresAt: now.Add(time.Hour), DailyQuotaUSD: 10, DailyWindowStart: &now,
					}}
					switch state {
					case "missing":
						repo.card = nil
					case "exhausted":
						repo.card.DailyUsageUSD = 10
					case "lookup_error":
						repo.err = errors.New("wallet unavailable")
					}
					billing.SetBalanceCardDeps(repo, nil)
					key := &service.APIKey{User: &service.User{ID: 1, Balance: cash}}
					est := &countingEstimator{cost: 1, priced: priced}
					for i := 0; i < 2; i++ {
						c := newInflightTestGinContext()
						done, err := reserveInflightBalance(c, billing, est, key, nil, tokenInflightEstimate("model", nil))
						t.Cleanup(done) // Both handler lifetimes overlap.
						switch {
						case state == "lookup_error":
							require.ErrorIs(t, err, service.ErrBillingServiceUnavailable)
							require.Zero(t, est.calls)
						case state == "funded":
							require.NoError(t, err)
							require.Zero(t, est.calls, "card admission must bypass even unpriced fail-closed")
							require.Nil(t, service.InflightReservationFromContext(c.Request.Context()))
						case !priced || i == 1:
							require.ErrorIs(t, err, service.ErrInsufficientBalance)
						default:
							require.NoError(t, err)
							require.NotNil(t, service.InflightReservationFromContext(c.Request.Context()))
						}
					}
					if priced && (state == "missing" || state == "exhausted") {
						require.Equal(t, 1, cache.count())
					} else {
						require.Zero(t, cache.count())
					}
				})
			}
		}
	}
}
