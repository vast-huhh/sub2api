-- Preserve the complete inbound header value for admin inspection/copying.
-- Nullable without a default/backfill; historical or absent values stay NULL.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS codex_turn_state TEXT;
