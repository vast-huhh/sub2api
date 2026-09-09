//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestBalanceCardCredit_AutoResetFourWeeks(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	now := timezone.Now()
	var userID, keyID, cardID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,balance)
		VALUES ('card-credit-test@example.invalid','test',100) RETURNING id`).Scan(&userID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name)
		VALUES ($1,'card-credit-test-key','test') RETURNING id`, userID).Scan(&keyID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO user_balance_cards(
		user_id,plan_name,card_type,validity_days,daily_quota_usd,weekly_quota_usd,monthly_quota_usd,
		starts_at,expires_at,daily_window_start,weekly_window_start,auto_reset_enabled)
		VALUES ($1,'test','month',30,10,50,200,$2,$3,$2,$2,true) RETURNING id`,
		userID, now, now.Add(30*24*time.Hour)).Scan(&cardID))
	for i := 0; i < 20; i++ {
		result := &service.UsageBillingApplyResult{}
		require.NoError(t, deductUsageBillingWallet(ctx, tx, &service.UsageBillingCommand{
			UserID: userID, APIKeyID: keyID, RequestID: fmt.Sprintf("credit-%d", i), BalanceCost: 10,
		}, result))
		require.Equal(t, 10.0, result.BalanceCardCost, "charge %d", i+1)
		require.Zero(t, result.CashBalanceCost, "charge %d must not fall back to cash", i+1)
		card, err := getBalanceCardTx(ctx, tx, cardID, timezone.Now())
		require.NoError(t, err)
		require.Equal(t, float64((i+1)*10), card.MonthlyUsageUSD)
		require.Equal(t, int64(i%5)*86400, card.WeeklyDailyAdvanceSeconds)
	}
	card, err := getBalanceCardTx(ctx, tx, cardID, timezone.Now())
	require.NoError(t, err)
	require.Equal(t, 19, card.ResetCount)
	require.Equal(t, 200.0, card.MonthlyUsageUSD)
	require.InDelta(t, 5*24*time.Hour.Seconds(), card.ExpiresAt.Sub(now).Seconds(), 10)
	var cash, total float64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=$1`, userID).Scan(&cash))
	require.Equal(t, 100.0, cash)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT sum(amount_usd) FROM balance_card_ledgers
		WHERE user_balance_card_id=$1 AND event_type='usage'`, cardID).Scan(&total))
	require.Equal(t, 200.0, total)

	// An actual seven-day rollover clears the credit for the new window.
	_, err = tx.ExecContext(ctx, `UPDATE user_balance_cards SET expires_at=$2 WHERE id=$1`, cardID, now.Add(60*24*time.Hour))
	require.NoError(t, err)
	later := now.Add(8 * 24 * time.Hour)
	require.NoError(t, normalizeUserBalanceCardsTx(ctx, tx, userID, later))
	card, err = getBalanceCardTx(ctx, tx, cardID, later)
	require.NoError(t, err)
	require.Zero(t, card.WeeklyDailyAdvanceSeconds)
	require.Zero(t, card.WeeklyUsageUSD)
	require.Equal(t, 200.0, card.MonthlyUsageUSD)
}

func TestBalanceCardCredit_MigrationBackfillsOnlyCurrentWeek(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	now := timezone.Now()
	var userID, cardID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash)
		VALUES ('card-credit-migration@example.invalid','test') RETURNING id`).Scan(&userID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO user_balance_cards(
		user_id,plan_name,card_type,validity_days,daily_quota_usd,weekly_quota_usd,monthly_quota_usd,
		starts_at,expires_at,weekly_window_start)
		VALUES ($1,'test','month',30,10,50,200,$2,$3,$2) RETURNING id`,
		userID, now, now.Add(30*24*time.Hour)).Scan(&cardID))
	for _, e := range []struct {
		window string
		at     time.Time
	}{
		{"daily", now.Add(-time.Hour)}, {"weekly", now},
		{"daily", now.Add(time.Hour)}, {"daily", now.Add(2 * time.Hour)},
	} {
		_, err := tx.ExecContext(ctx, `INSERT INTO balance_card_ledgers(user_balance_card_id,user_id,
			event_type,expires_at_before,expires_at_after,metadata,created_at)
			VALUES ($1,$2,'auto_reset',$3,$4,jsonb_build_object('window',$5::text),$6)`,
			cardID, userID, now.Add(30*24*time.Hour), now.Add(29*24*time.Hour), e.window, e.at)
		require.NoError(t, err)
	}
	sql, err := migrations.FS.ReadFile("237_balance_card_daily_advance_credit.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(sql))
		require.NoError(t, err)
		card, err := getBalanceCardTx(ctx, tx, cardID, now)
		require.NoError(t, err)
		require.Equal(t, int64(2*86400), card.WeeklyDailyAdvanceSeconds)
		require.WithinDuration(t, now.Add(30*24*time.Hour), card.ExpiresAt, time.Microsecond)
	}
}
