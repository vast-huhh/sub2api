-- Store only the inbound X-Codex-Turn-State value length in bytes.
-- NULL = historical/not captured; 0 = captured request without a value.
-- No default/backfill, to avoid rewriting the usage log table.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS codex_turn_state_length INTEGER;
