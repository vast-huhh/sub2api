-- Daily resets pay validity in advance within the current weekly allowance.
-- Keep this separate from weekly_window_start: paying validity must not silently
-- clear weekly usage or move the real-time natural weekly reset boundary.
ALTER TABLE user_balance_cards
    ADD COLUMN IF NOT EXISTS weekly_daily_advance_seconds BIGINT NOT NULL DEFAULT 0
    CHECK (weekly_daily_advance_seconds >= 0);

-- Preserve credit for existing cards. Reset ledger timestamps are transaction
-- timestamps; daily events follow the start of their current weekly window.
UPDATE user_balance_cards c
SET weekly_daily_advance_seconds = COALESCE((
    SELECT SUM(EXTRACT(EPOCH FROM (l.expires_at_before-l.expires_at_after)))::BIGINT
    FROM balance_card_ledgers l
    WHERE l.user_balance_card_id=c.id
      AND l.event_type IN ('auto_reset','manual_reset')
      AND l.metadata->>'window'='daily'
      AND l.created_at >= c.weekly_window_start
      AND l.expires_at_before > l.expires_at_after
), 0)
WHERE c.card_type='month' AND c.weekly_quota_usd>0;
