-- Hide deleted balance cards without destroying their billing and audit history.

ALTER TABLE user_balance_cards
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

ALTER TABLE user_balance_cards
    ADD COLUMN IF NOT EXISTS deleted_by BIGINT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_user_balance_cards_visible_user_status
    ON user_balance_cards (user_id, status, starts_at, expires_at)
    WHERE deleted_at IS NULL;
