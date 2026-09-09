package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBalanceCardDailyAdvanceCreditMigration(t *testing.T) {
	content, err := FS.ReadFile("237_balance_card_daily_advance_credit.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS weekly_daily_advance_seconds BIGINT NOT NULL DEFAULT 0")
	require.Contains(t, sql, "l.created_at >= c.weekly_window_start")
	require.Contains(t, sql, "l.metadata->>'window'='daily'")
	require.NotContains(t, sql, "SET expires_at")
	require.NotContains(t, sql, "UPDATE users")
}

func TestBalanceCardsMigrationCreatesIndependentWallet(t *testing.T) {
	content, err := FS.ReadFile("231_balance_cards.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS balance_card_plans")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS user_balance_cards")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS balance_card_ledgers")
	require.Contains(t, sql, "WHERE status = 'active'")
	require.Contains(t, sql, "fallback_enabled BOOLEAN NOT NULL DEFAULT TRUE")
	require.Contains(t, sql, "auto_reset_enabled BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "balance_card_ledgers_operation_uq")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS balance_card_cost")
	require.NotContains(t, sql, "group_id")
}

func TestBalanceCardPeriodQuotaMigrationAddsSubscriptionStyleLimits(t *testing.T) {
	content, err := FS.ReadFile("232_balance_card_period_quotas.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE balance_card_plans")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS weekly_quota_usd")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS monthly_quota_usd")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS weekly_window_start")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS weekly_usage_usd")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS monthly_usage_usd")
	require.Contains(t, sql, "new limit disabled for cards issued before this migration")
}

func TestBalanceCardUnlimitedQuotaMigrationAllowsZeroDailyQuota(t *testing.T) {
	content, err := FS.ReadFile("233_balance_card_unlimited_quotas.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS balance_card_plans_daily_quota_check")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_balance_cards_daily_quota_check")
	require.Equal(t, 2, strings.Count(sql, "CHECK (daily_quota_usd >= 0)"))
}

func TestBalanceCardSoftDeleteMigrationPreservesAuditHistory(t *testing.T) {
	content, err := FS.ReadFile("234_balance_card_soft_delete.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS deleted_by BIGINT REFERENCES users(id) ON DELETE SET NULL")
	require.Contains(t, sql, "WHERE deleted_at IS NULL")
	require.NotContains(t, sql, "DELETE FROM balance_card_ledgers")
}

func TestBalanceCardRedeemCodeMigrationLinksPlanSnapshot(t *testing.T) {
	content, err := FS.ReadFile("235_balance_card_redeem_codes.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE redeem_codes")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS balance_card_plan_id BIGINT")
	require.Contains(t, sql, "REFERENCES balance_card_plans(id) ON DELETE SET NULL")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS balance_card_plan_name VARCHAR(100) NOT NULL DEFAULT ''")
	require.Contains(t, sql, "idx_redeem_codes_balance_card_plan_id")
}
