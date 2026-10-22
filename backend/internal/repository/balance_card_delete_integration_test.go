//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBalanceCardDeletionPreservesIssuedCardsAndHistory(t *testing.T) {
	ctx := context.Background()
	repo := NewBalanceCardRepository(integrationDB)
	svc := service.NewBalanceCardService(repo, nil)
	price, listed := 20.0, true
	input := &service.CreateBalanceCardPlanInput{
		Name: "Deletion lifecycle", ValidityDays: 30, DailyQuotaUSD: 10,
		PriceCNY: &price, ForSale: &listed,
	}
	plan, err := svc.CreatePlan(ctx, input)
	require.NoError(t, err)
	var userID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO users(email,password_hash)
		VALUES ('card-deletion@example.invalid','test') RETURNING id`).Scan(&userID))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, userID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM balance_card_plans WHERE id=$1`, plan.ID)
		require.NoError(t, err)
	})
	card, err := repo.AssignCard(ctx, &service.AssignBalanceCardInput{UserID: userID, PlanID: plan.ID}, time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, svc.Delete(ctx, card.ID, userID), service.ErrBalanceCardOperationConflict)
	var before string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_jsonb(c)::text FROM user_balance_cards c WHERE id=$1`, card.ID).Scan(&before))

	require.NoError(t, svc.DeletePlan(ctx, plan.ID, userID))
	var after string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT to_jsonb(c)::text FROM user_balance_cards c WHERE id=$1`, card.ID).Scan(&after))
	require.Equal(t, before, after, "plan deletion must not change issued cards")
	var deleted, forSale bool
	var actorID int64
	var status string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL, deleted_by, status, for_sale
		FROM balance_card_plans WHERE id=$1`, plan.ID).Scan(&deleted, &actorID, &status, &forSale))
	require.True(t, deleted)
	require.Equal(t, userID, actorID)
	require.Equal(t, service.BalanceCardPlanStatusInactive, status)
	require.False(t, forSale)
	_, err = svc.GetPlan(ctx, plan.ID)
	require.ErrorIs(t, err, service.ErrBalanceCardPlanNotFound)
	_, err = svc.UpdatePlan(ctx, plan.ID, input)
	require.ErrorIs(t, err, service.ErrBalanceCardPlanNotFound)
	require.ErrorIs(t, svc.DeletePlan(ctx, plan.ID, userID), service.ErrBalanceCardPlanNotFound)
	for _, includeInactive := range []bool{false, true} {
		plans, err := svc.ListPlans(ctx, includeInactive)
		require.NoError(t, err)
		for _, item := range plans {
			require.NotEqual(t, plan.ID, item.ID)
		}
	}
	products, err := svc.ListProducts(ctx)
	require.NoError(t, err)
	for _, item := range products {
		require.NotEqual(t, plan.ID, item.ID)
	}
	_, err = repo.AssignCard(ctx, &service.AssignBalanceCardInput{UserID: userID, PlanID: plan.ID}, time.Now())
	require.ErrorIs(t, err, service.ErrBalanceCardPlanNotFound)
	_, err = repo.RedeemCard(ctx, 0, userID, plan.ID, "unused-code", time.Now())
	require.ErrorIs(t, err, service.ErrBalanceCardPlanNotFound)
	wallet, err := repo.GetWalletSnapshot(ctx, userID, time.Now())
	require.NoError(t, err)
	require.Equal(t, card.ID, wallet.CardID, "issued card must remain usable")

	// Admin listing computes expiry even when the stored status is still active.
	now := time.Now()
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_balance_cards SET starts_at=$2, expires_at=$3 WHERE id=$1`,
		card.ID, now.Add(-48*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)
	items, _, err := repo.ListCards(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, service.BalanceCardListFilter{UserID: &userID}, now)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, service.BalanceCardStatusExpired, items[0].Status)
	var ledgerCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM balance_card_ledgers WHERE user_balance_card_id=$1`, card.ID).Scan(&ledgerCount))
	require.Positive(t, ledgerCount)
	require.NoError(t, svc.Delete(ctx, card.ID, userID))
	items, page, err := repo.ListCards(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, service.BalanceCardListFilter{UserID: &userID}, now)
	require.NoError(t, err)
	require.Empty(t, items)
	require.Zero(t, page.Total)
	items, err = repo.ListUserCards(ctx, userID, now)
	require.NoError(t, err)
	require.Empty(t, items)
	_, err = repo.GetCard(ctx, card.ID, now)
	require.ErrorIs(t, err, service.ErrBalanceCardNotFound)
	require.ErrorIs(t, svc.Delete(ctx, card.ID, userID), service.ErrBalanceCardNotFound)
	var remainingLedgerCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM balance_card_ledgers WHERE user_balance_card_id=$1`, card.ID).Scan(&remainingLedgerCount))
	require.Equal(t, ledgerCount, remainingLedgerCount)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL, deleted_by FROM user_balance_cards WHERE id=$1`, card.ID).Scan(&deleted, &actorID))
	require.True(t, deleted)
	require.Equal(t, userID, actorID)
}
