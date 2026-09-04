-- A zero balance-card quota means that allowance dimension is unlimited.
-- Period quotas already allow zero; relax the original daily constraints to
-- use the same convention for both plans and issued-card snapshots.

ALTER TABLE balance_card_plans
    DROP CONSTRAINT IF EXISTS balance_card_plans_daily_quota_check;
ALTER TABLE balance_card_plans
    ADD CONSTRAINT balance_card_plans_daily_quota_check
    CHECK (daily_quota_usd >= 0);

ALTER TABLE user_balance_cards
    DROP CONSTRAINT IF EXISTS user_balance_cards_daily_quota_check;
ALTER TABLE user_balance_cards
    ADD CONSTRAINT user_balance_cards_daily_quota_check
    CHECK (daily_quota_usd >= 0);
