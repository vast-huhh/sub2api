-- Publish balance-card plans as products without changing issued cards.
ALTER TABLE balance_card_plans
    ADD COLUMN IF NOT EXISTS price_cny NUMERIC(12, 2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS for_sale BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS purchase_url TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'balance_card_plans_price_check' AND conrelid = 'balance_card_plans'::regclass) THEN
        ALTER TABLE balance_card_plans ADD CONSTRAINT balance_card_plans_price_check
            CHECK (price_cny >= 0 AND (NOT for_sale OR price_cny > 0));
    END IF;
END $$;
