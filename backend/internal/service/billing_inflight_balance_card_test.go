//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type inflightCardRepo struct {
	BalanceCardRepository
	card *BalanceCardWalletSnapshot
	err  error
}

func (r *inflightCardRepo) GetWalletSnapshot(context.Context, int64, time.Time) (*BalanceCardWalletSnapshot, error) {
	return r.card, r.err
}

func TestInflightReservation_BalanceCardCompatibility(t *testing.T) {
	for _, cash := range []float64{0, -5, 1.5} {
		for _, state := range []string{"funded", "resettable", "missing", "exhausted", "expired", "lookup_error"} {
			t.Run(fmt.Sprintf("%s/cash=%g", state, cash), func(t *testing.T) {
				cache := newMemInflightCache(cash)
				svc := newInflightSvc(t, cache, 60)
				card := &BalanceCardWalletSnapshot{ExpiresAt: time.Now().Add(72 * time.Hour), DailyQuotaUSD: 10}
				repo := &inflightCardRepo{card: card}
				switch state {
				case "resettable":
					card.DailyUsageUSD = 10
					now := time.Now()
					card.DailyWindowStart = &now
					card.ValidityDays = 3
					card.AutoResetEnabled = true
					card.MaxResetCount = 2
				case "missing":
					repo.card = nil
				case "exhausted":
					card.DailyUsageUSD = 10
					now := time.Now()
					card.DailyWindowStart = &now
				case "expired":
					card.ExpiresAt = time.Now().Add(-time.Hour)
				case "lookup_error":
					repo.err = errors.New("wallet unavailable")
				}
				svc.SetBalanceCardDeps(repo, nil)
				user := &User{ID: 1, Balance: cash}
				first, err := svc.ReserveInflight(context.Background(), user, nil, nil, 1)
				if state == "lookup_error" {
					require.ErrorIs(t, err, ErrBillingServiceUnavailable)
					require.Nil(t, first)
					require.Zero(t, cache.count())
					return
				}
				require.NoError(t, err)
				t.Cleanup(first.HandlerDone)
				// Keep the first request alive while exercising the other public wrapper.
				done, err := svc.ReserveInflightBalance(context.Background(), user, nil, nil, 1)
				t.Cleanup(done)
				if state == "funded" || state == "resettable" {
					require.NoError(t, err)
					require.Nil(t, first)
					require.Zero(t, cache.count())
				} else {
					// Eligibility is checked by callers; cash reservation retains its
					// first-request exception and rejects the overlapping request.
					require.NotNil(t, first)
					require.ErrorIs(t, err, ErrInsufficientBalance)
					require.Equal(t, 1, cache.count())
				}
			})
		}
	}
}
