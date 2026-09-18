-- Response state is distinct from the inbound state saved by versions <= r2.
-- Keep the legacy columns intact for rollback; never relabel inbound values.
-- Nullable, no default/backfill: historical response values remain unknown.
ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS response_codex_turn_state TEXT,
  ADD COLUMN IF NOT EXISTS response_codex_turn_state_length INTEGER;
