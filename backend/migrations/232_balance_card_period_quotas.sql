-- Add subscription-style period limits to the independent balance-card wallet.
-- Daily quota remains the resettable allowance. Week cards use weekly_quota_usd
-- as the whole-card cap; month cards use both a weekly cap and a whole-card
-- monthly cap. A month card's weekly window is a rolling seven-day window
-- anchored at activation; its monthly cap spans the whole card. Zero keeps the
-- new limit disabled for cards issued before this migration.

ALTER TABLE balance_card_plans
    ADD COLUMN IF NOT EXISTS weekly_quota_usd DECIMAL(20, 8) NOT NULL DEFAULT 0;
ALTER TABLE balance_card_plans
    ADD COLUMN IF NOT EXISTS monthly_quota_usd DECIMAL(20, 8) NOT NULL DEFAULT 0;

ALTER TABLE user_balance_cards
    ADD COLUMN IF NOT EXISTS weekly_quota_usd DECIMAL(20, 8) NOT NULL DEFAULT 0;
ALTER TABLE user_balance_cards
    ADD COLUMN IF NOT EXISTS monthly_quota_usd DECIMAL(20, 8) NOT NULL DEFAULT 0;
ALTER TABLE user_balance_cards
    ADD COLUMN IF NOT EXISTS weekly_window_start TIMESTAMPTZ;
ALTER TABLE user_balance_cards
    ADD COLUMN IF NOT EXISTS weekly_usage_usd DECIMAL(20, 8) NOT NULL DEFAULT 0;
ALTER TABLE user_balance_cards
    ADD COLUMN IF NOT EXISTS monthly_usage_usd DECIMAL(20, 8) NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'balance_card_plans_period_quota_check'
          AND conrelid = 'balance_card_plans'::regclass
    ) THEN
        ALTER TABLE balance_card_plans
            ADD CONSTRAINT balance_card_plans_period_quota_check
            CHECK (weekly_quota_usd >= 0 AND monthly_quota_usd >= 0);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'user_balance_cards_period_quota_check'
          AND conrelid = 'user_balance_cards'::regclass
    ) THEN
        ALTER TABLE user_balance_cards
            ADD CONSTRAINT user_balance_cards_period_quota_check
            CHECK (
                weekly_quota_usd >= 0
                AND monthly_quota_usd >= 0
                AND weekly_usage_usd >= 0
                AND monthly_usage_usd >= 0
            );
    END IF;
END
$$;
