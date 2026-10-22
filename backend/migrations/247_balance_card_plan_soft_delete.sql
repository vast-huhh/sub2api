-- Keep plan references and issued-card snapshots after removing a plan from sale.
ALTER TABLE balance_card_plans
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_by BIGINT REFERENCES users(id) ON DELETE SET NULL;
