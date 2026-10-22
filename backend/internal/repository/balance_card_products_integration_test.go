//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestBalanceCardProductsMigrationPreservesIssuedCards(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	var planID, userID, cardID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO balance_card_plans
		(name,card_type,validity_days,daily_quota_usd) VALUES ('legacy','month',30,10) RETURNING id`).Scan(&planID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash)
		VALUES ('card-product-migration@example.invalid','test') RETURNING id`).Scan(&userID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO user_balance_cards
		(user_id,plan_id,plan_name,card_type,validity_days,daily_quota_usd,starts_at,expires_at)
		VALUES ($1,$2,'legacy','month',30,10,NOW(),NOW()+INTERVAL '30 days') RETURNING id`, userID, planID).Scan(&cardID))
	var before string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_jsonb(c)::text FROM user_balance_cards c WHERE id=$1`, cardID).Scan(&before))
	// Recreate the old plan schema within this rollback-only test transaction.
	_, err := tx.ExecContext(ctx, `ALTER TABLE balance_card_plans DROP CONSTRAINT balance_card_plans_price_check,
		DROP COLUMN price_cny, DROP COLUMN for_sale, DROP COLUMN purchase_url`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("246_balance_card_products.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
		plan, err := scanBalanceCardPlan(tx.QueryRowContext(ctx, `SELECT `+balanceCardPlanColumns+` FROM balance_card_plans WHERE id=$1`, planID))
		require.NoError(t, err)
		require.Equal(t, "legacy", plan.Name)
		require.Equal(t, 10.0, plan.DailyQuotaUSD)
		require.Zero(t, plan.PriceCNY)
		require.False(t, plan.ForSale)
		require.Empty(t, plan.PurchaseURL)
	}
	_, err = tx.ExecContext(ctx, `UPDATE balance_card_plans SET price_cny=19.99,for_sale=true,
		purchase_url='https://store.example/card',daily_quota_usd=20 WHERE id=$1`, planID)
	require.NoError(t, err)
	var after string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_jsonb(c)::text FROM user_balance_cards c WHERE id=$1`, cardID).Scan(&after))
	require.Equal(t, before, after)
}

func TestBalanceCardProductsRepositoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := NewBalanceCardRepository(integrationDB)
	svc := service.NewBalanceCardService(repo, nil)
	price, listed, link := 19.99, true, "https://store.example/card"
	plan, err := svc.CreatePlan(ctx, &service.CreateBalanceCardPlanInput{
		Name: "Product round trip", ValidityDays: 30, DailyQuotaUSD: 10,
		PriceCNY: &price, ForSale: &listed, PurchaseURL: &link,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(context.Background(), `DELETE FROM balance_card_plans WHERE id=$1`, plan.ID)
		require.NoError(t, cleanupErr)
	})
	// An older client omits the new fields when editing quota settings.
	updated, err := svc.UpdatePlan(ctx, plan.ID, &service.UpdateBalanceCardPlanInput{
		Name: plan.Name, ValidityDays: 30, DailyQuotaUSD: 20,
	})
	require.NoError(t, err)
	require.Equal(t, price, updated.PriceCNY)
	require.True(t, updated.ForSale)
	require.Equal(t, link, updated.PurchaseURL)
	require.Equal(t, 20.0, updated.DailyQuotaUSD)
	products, err := svc.ListProducts(ctx)
	require.NoError(t, err)
	found := false
	for _, product := range products {
		if product.ID == plan.ID {
			found = true
			require.Equal(t, price, product.PriceCNY)
			require.Equal(t, link, product.PurchaseURL)
		}
	}
	require.True(t, found)
}
