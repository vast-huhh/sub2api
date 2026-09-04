-- Independent balance-card wallet.
-- Balance cards stay in the standard/balance billing mode and are not tied to groups.

CREATE TABLE IF NOT EXISTS balance_card_plans (
    id                    BIGSERIAL PRIMARY KEY,
    name                  VARCHAR(100) NOT NULL,
    description           TEXT NOT NULL DEFAULT '',
    card_type             VARCHAR(20) NOT NULL DEFAULT 'month',
    validity_days         INT NOT NULL,
    daily_quota_usd       DECIMAL(20, 8) NOT NULL,
    fallback_default      BOOLEAN NOT NULL DEFAULT TRUE,
    auto_reset_default    BOOLEAN NOT NULL DEFAULT FALSE,
    max_reset_count       INT NOT NULL DEFAULT 20,
    status                VARCHAR(20) NOT NULL DEFAULT 'active',
    sort_order            INT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT balance_card_plans_card_type_check
        CHECK (card_type IN ('day', 'week', 'month', 'custom')),
    CONSTRAINT balance_card_plans_validity_days_check
        CHECK (validity_days > 0 AND validity_days <= 36500),
    CONSTRAINT balance_card_plans_daily_quota_check
        CHECK (daily_quota_usd > 0),
    CONSTRAINT balance_card_plans_max_reset_count_check
        CHECK (max_reset_count >= 0 AND max_reset_count <= 1000),
    CONSTRAINT balance_card_plans_status_check
        CHECK (status IN ('active', 'inactive'))
);

CREATE UNIQUE INDEX IF NOT EXISTS balance_card_plans_name_uq
    ON balance_card_plans (LOWER(name));
CREATE INDEX IF NOT EXISTS idx_balance_card_plans_status_sort
    ON balance_card_plans (status, sort_order, id);

CREATE TABLE IF NOT EXISTS user_balance_cards (
    id                    BIGSERIAL PRIMARY KEY,
    user_id               BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id               BIGINT REFERENCES balance_card_plans(id) ON DELETE SET NULL,

    -- Snapshot product fields so later plan edits never rewrite an issued card.
    plan_name             VARCHAR(100) NOT NULL,
    card_type             VARCHAR(20) NOT NULL,
    validity_days         INT NOT NULL,
    daily_quota_usd       DECIMAL(20, 8) NOT NULL,
    max_reset_count       INT NOT NULL DEFAULT 20,

    starts_at             TIMESTAMPTZ NOT NULL,
    expires_at            TIMESTAMPTZ NOT NULL,
    status                VARCHAR(20) NOT NULL DEFAULT 'active',
    daily_window_start    TIMESTAMPTZ,
    daily_usage_usd       DECIMAL(20, 8) NOT NULL DEFAULT 0,
    fallback_enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    auto_reset_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    reset_count           INT NOT NULL DEFAULT 0,

    assigned_by           BIGINT REFERENCES users(id) ON DELETE SET NULL,
    assigned_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at          TIMESTAMPTZ,
    notes                 TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_balance_cards_card_type_check
        CHECK (card_type IN ('day', 'week', 'month', 'custom')),
    CONSTRAINT user_balance_cards_status_check
        CHECK (status IN ('pending', 'active', 'expired', 'suspended', 'revoked')),
    CONSTRAINT user_balance_cards_validity_days_check
        CHECK (validity_days > 0 AND validity_days <= 36500),
    CONSTRAINT user_balance_cards_daily_quota_check
        CHECK (daily_quota_usd > 0),
    CONSTRAINT user_balance_cards_usage_check
        CHECK (daily_usage_usd >= 0),
    CONSTRAINT user_balance_cards_reset_count_check
        CHECK (reset_count >= 0 AND max_reset_count >= 0),
    CONSTRAINT user_balance_cards_term_check
        CHECK (expires_at > starts_at)
);

-- A balance card is global to standard balance billing, so only one can be active.
CREATE UNIQUE INDEX IF NOT EXISTS user_balance_cards_one_active_uq
    ON user_balance_cards (user_id)
    WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_user_balance_cards_user_status_term
    ON user_balance_cards (user_id, status, starts_at, expires_at);
CREATE INDEX IF NOT EXISTS idx_user_balance_cards_plan_id
    ON user_balance_cards (plan_id);
CREATE INDEX IF NOT EXISTS idx_user_balance_cards_expires_at
    ON user_balance_cards (expires_at);

CREATE TABLE IF NOT EXISTS balance_card_ledgers (
    id                    BIGSERIAL PRIMARY KEY,
    user_balance_card_id  BIGINT NOT NULL REFERENCES user_balance_cards(id) ON DELETE CASCADE,
    user_id               BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type            VARCHAR(30) NOT NULL,
    amount_usd            DECIMAL(20, 8) NOT NULL DEFAULT 0,
    daily_usage_before    DECIMAL(20, 8) NOT NULL DEFAULT 0,
    daily_usage_after     DECIMAL(20, 8) NOT NULL DEFAULT 0,
    expires_at_before     TIMESTAMPTZ,
    expires_at_after      TIMESTAMPTZ,
    request_id            VARCHAR(128),
    api_key_id            BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    operation_key         VARCHAR(128),
    actor_id              BIGINT REFERENCES users(id) ON DELETE SET NULL,
    notes                 TEXT NOT NULL DEFAULT '',
    metadata              JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT balance_card_ledgers_event_type_check CHECK (event_type IN (
        'assign', 'activate', 'usage', 'natural_reset', 'manual_reset',
        'auto_reset', 'extend', 'revoke', 'restore', 'preference_update'
    ))
);

CREATE INDEX IF NOT EXISTS idx_balance_card_ledgers_card_created
    ON balance_card_ledgers (user_balance_card_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_balance_card_ledgers_user_created
    ON balance_card_ledgers (user_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS balance_card_ledgers_usage_request_uq
    ON balance_card_ledgers (request_id, api_key_id, event_type)
    WHERE request_id IS NOT NULL AND api_key_id IS NOT NULL AND event_type = 'usage';
CREATE UNIQUE INDEX IF NOT EXISTS balance_card_ledgers_operation_uq
    ON balance_card_ledgers (operation_key)
    WHERE operation_key IS NOT NULL;

-- Optional audit split on the existing balance-billed usage log.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS balance_card_id BIGINT REFERENCES user_balance_cards(id) ON DELETE SET NULL;
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS balance_card_cost DECIMAL(20, 10) NOT NULL DEFAULT 0;
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS cash_balance_cost DECIMAL(20, 10) NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_usage_logs_balance_card_id
    ON usage_logs (balance_card_id)
    WHERE balance_card_id IS NOT NULL;
