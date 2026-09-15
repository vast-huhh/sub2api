-- Custom: archive unlimited quota rows without losing historical usage counters.
-- Soft-deleted rows are excluded by the repository, just like missing rows.
-- Preserve existing archives and configured limits, including explicit zero.

UPDATE user_platform_quotas
   SET deleted_at = CURRENT_TIMESTAMP
 WHERE daily_limit_usd IS NULL
   AND weekly_limit_usd IS NULL
   AND monthly_limit_usd IS NULL
   AND deleted_at IS NULL;
