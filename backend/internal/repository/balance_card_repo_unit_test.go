//go:build unit

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBalanceCardListCards_NoFiltersDoesNotBindUnusedCountArgument(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM user_balance_cards c WHERE c\.deleted_at IS NULL`).
		WithArgs().
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`(?s)SELECT.*FROM user_balance_cards c.*LIMIT \$2 OFFSET \$3`).
		WithArgs(sqlmock.AnyArg(), 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "email", "plan_id", "plan_name", "card_type", "validity_days",
			"daily_quota_usd", "weekly_quota_usd", "monthly_quota_usd", "max_reset_count",
			"starts_at", "expires_at", "status", "daily_window_start", "daily_usage_usd",
			"weekly_window_start", "weekly_usage_usd", "monthly_usage_usd", "fallback_enabled", "auto_reset_enabled",
			"reset_count", "assigned_by", "assigned_at", "activated_at", "notes", "created_at", "updated_at",
		}))

	repo := &balanceCardRepository{db: db}
	items, page, err := repo.ListCards(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, service.BalanceCardListFilter{}, time.Now())
	require.NoError(t, err)
	require.Empty(t, items)
	require.Zero(t, page.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCardDeleteCard_SoftDeletesRevokedCard(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT user_id, status FROM user_balance_cards.*deleted_at IS NULL FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "status"}).AddRow(int64(20), service.BalanceCardStatusRevoked))
	mock.ExpectExec(`(?s)UPDATE user_balance_cards.*SET deleted_at=\$2, deleted_by=NULLIF\(\$3,0\), updated_at=\$2`).
		WithArgs(int64(7), now, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := &balanceCardRepository{db: db}
	userID, err := repo.DeleteCard(context.Background(), 7, 1, now)
	require.NoError(t, err)
	require.Equal(t, int64(20), userID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCardDeleteCard_RejectsCardThatIsNotRevoked(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT user_id, status FROM user_balance_cards.*deleted_at IS NULL FOR UPDATE`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "status"}).AddRow(int64(20), service.BalanceCardStatusActive))
	mock.ExpectRollback()

	repo := &balanceCardRepository{db: db}
	_, err = repo.DeleteCard(context.Background(), 7, 1, time.Now())
	require.ErrorIs(t, err, service.ErrBalanceCardOperationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCardRedeemCardRollsBackCodeWhenIssuanceFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT.*FROM balance_card_plans WHERE id=\$1`).
		WithArgs(int64(9)).
		WillReturnRows(balanceCardPlanRows().AddRow(
			int64(9), "Monthly 1000", "", service.BalanceCardTypeMonth, 30,
			60.0, 0.0, 1000.0, true, true, 20, service.StatusActive, 0, now, now,
		))
	mock.ExpectExec(`(?s)UPDATE redeem_codes.*WHERE id=\$1 AND status='unused'`).
		WithArgs(int64(41), int64(12), now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT 1 FROM users WHERE id=\$1 AND deleted_at IS NULL`).
		WithArgs(int64(12)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	repo := &balanceCardRepository{db: db}
	_, err = repo.RedeemCard(context.Background(), 41, 12, 9, "CARD-CODE", now)

	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceCardResetDaily_AdvancesExhaustedMonthCardWeekByRemainingTerm(t *testing.T) {
	for _, credit := range []int64{0, 86400, 4 * 86400} {
		t.Run(fmt.Sprint(credit), func(t *testing.T) { testManualWeeklyResetCredit(t, credit) })
	}
}

func testManualWeeklyResetCredit(t *testing.T, credit int64) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.Local)
	weekStart := now.Add(-5 * 24 * time.Hour)
	startsAt := weekStart
	expiresAt := now.Add(20 * 24 * time.Hour)
	duration := max(time.Duration(0), 2*24*time.Hour-time.Duration(credit)*time.Second)
	newExpiresAt := expiresAt.Add(-duration)
	createdAt := startsAt

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT user_id FROM user_balance_cards WHERE id=\$1 AND user_id=\$2`).
		WithArgs(int64(9), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(int64(42)))
	mock.ExpectExec(`SELECT pg_advisory_xact_lock\(\$1\)`).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE user_balance_cards\s+SET status='expired'`).
		WithArgs(int64(42), now).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)SELECT id FROM user_balance_cards\s+WHERE user_id=\$1 AND status='active'`).
		WithArgs(int64(42), now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(9)))
	mock.ExpectQuery(`(?s)SELECT card_type, validity_days, daily_window_start,\s+daily_usage_usd::double precision, weekly_quota_usd::double precision,\s+weekly_window_start, weekly_usage_usd::double precision`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{
			"card_type", "validity_days", "daily_window_start", "daily_usage_usd",
			"weekly_quota_usd", "weekly_window_start", "weekly_usage_usd",
		}).AddRow("month", 30, now, 40.0, 300.0, weekStart, 300.0))
	mock.ExpectQuery(`SELECT user_balance_card_id FROM balance_card_ledgers`).
		WithArgs("week-reset-op").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`(?s)SELECT status, card_type, daily_usage_usd::double precision,.*FROM user_balance_cards WHERE id=\$1 FOR UPDATE`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "card_type", "daily_usage_usd", "daily_quota_usd", "weekly_quota_usd",
			"weekly_usage_usd", "weekly_window_start", "monthly_quota_usd", "monthly_usage_usd",
			"reset_count", "max_reset_count", "expires_at", "weekly_daily_advance_seconds",
		}).AddRow("active", "month", 40.0, 60.0, 300.0, 300.0, weekStart, 1000.0, 300.0, 0, 20, expiresAt, credit))
	mock.ExpectExec(`(?s)UPDATE user_balance_cards SET\s+daily_usage_usd=0, daily_window_start=\$2,\s+weekly_usage_usd=0, weekly_window_start=\$3`).
		WithArgs(int64(9), sqlmock.AnyArg(), now, newExpiresAt, now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if duration > 0 {
		mock.ExpectExec(`(?s)UPDATE user_balance_cards SET\s+starts_at=starts_at \+ \(\$1 \* INTERVAL '1 second'\)`).
			WithArgs(-duration.Seconds(), int64(42), int64(9), now, expiresAt).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectExec(`(?s)INSERT INTO balance_card_ledgers`).
		WithArgs(int64(9), int64(42), "manual_reset", 0.0, 40.0, 0.0,
			expiresAt, newExpiresAt, sqlmock.AnyArg(), sqlmock.AnyArg(), "week-reset-op", int64(42), "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`(?s)SELECT.*FROM user_balance_cards c JOIN users u ON u.id=c.user_id.*WHERE c.id=\$2`).
		WithArgs(now, int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "email", "plan_id", "plan_name", "card_type", "validity_days",
			"daily_quota_usd", "weekly_quota_usd", "monthly_quota_usd", "max_reset_count",
			"starts_at", "expires_at", "status", "daily_window_start", "daily_usage_usd",
			"weekly_window_start", "weekly_usage_usd", "monthly_usage_usd", "fallback_enabled",
			"auto_reset_enabled", "reset_count", "assigned_by", "assigned_at", "activated_at",
			"notes", "created_at", "updated_at", "weekly_daily_advance_seconds",
		}).AddRow(int64(9), int64(42), "user@example.com", int64(1), "Monthly", "month", 30,
			60.0, 300.0, 1000.0, 20, startsAt, newExpiresAt, "active", now, 0.0,
			now, 0.0, 300.0, true, true, 1, int64(1), startsAt, startsAt, "", createdAt, now, 0))
	mock.ExpectCommit()

	repo := &balanceCardRepository{db: db}
	card, err := repo.ResetDaily(ctx, 9, 42, 42, "week-reset-op", now)
	require.NoError(t, err)
	require.Equal(t, newExpiresAt, card.ExpiresAt)
	require.Zero(t, card.DailyUsageUSD)
	require.Zero(t, card.WeeklyUsageUSD)
	require.InDelta(t, 300, card.MonthlyUsageUSD, 0.000001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func balanceCardPlanRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "name", "description", "card_type", "validity_days",
		"daily_quota_usd", "weekly_quota_usd", "monthly_quota_usd",
		"fallback_default", "auto_reset_default", "max_reset_count", "status",
		"sort_order", "created_at", "updated_at",
	})
}
