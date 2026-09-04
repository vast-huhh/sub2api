-- Allow standard redeem codes to grant an independently managed balance card.

ALTER TABLE redeem_codes
    ADD COLUMN IF NOT EXISTS balance_card_plan_id BIGINT REFERENCES balance_card_plans(id) ON DELETE SET NULL;

ALTER TABLE redeem_codes
    ADD COLUMN IF NOT EXISTS balance_card_plan_name VARCHAR(100) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_redeem_codes_balance_card_plan_id
    ON redeem_codes (balance_card_plan_id)
    WHERE balance_card_plan_id IS NOT NULL;
