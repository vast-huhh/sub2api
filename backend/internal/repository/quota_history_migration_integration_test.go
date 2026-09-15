//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestQuotaHistoryMigrationPreservesRowsAndCounters(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	var userID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash)
		VALUES ('quota-history@example.invalid','test') RETURNING id`).Scan(&userID))
	_, err := tx.ExecContext(ctx, `INSERT INTO user_platform_quotas
		(user_id,platform,daily_limit_usd,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,deleted_at)
		VALUES ($1,'openai',NULL,1,2,3,NULL), ($1,'anthropic',0,4,5,6,NULL),
		($1,'gemini',NULL,7,8,9,'2026-01-01'::timestamptz)`, userID)
	require.NoError(t, err)
	var before string
	const snapshot = `SELECT jsonb_agg(to_jsonb(q)-'deleted_at' ORDER BY id)::text
		FROM user_platform_quotas q WHERE user_id=$1`
	require.NoError(t, tx.QueryRowContext(ctx, snapshot, userID).Scan(&before))
	migration, err := migrations.FS.ReadFile("238_purge_unlimited_user_platform_quotas.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
		var after string
		require.NoError(t, tx.QueryRowContext(ctx, snapshot, userID).Scan(&after))
		require.Equal(t, before, after)
		var correct int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM user_platform_quotas
			WHERE user_id=$1 AND ((platform='openai' AND deleted_at IS NOT NULL)
			OR (platform='anthropic' AND deleted_at IS NULL)
			OR (platform='gemini' AND deleted_at='2026-01-01'::timestamptz))`, userID).Scan(&correct))
		require.Equal(t, 3, correct)
	}
}
