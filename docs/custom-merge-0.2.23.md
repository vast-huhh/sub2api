# Custom merge: upstream 0.2.23

Baseline: custom `b2e66b4d96ce788e835e51f01522a485a28e0cbf`.
Upstream: `2af3f1e15c98c893a89255a037f551046883b8c0`.
Backup branch: `backup/pre-merge-20261118-0.2.23`.

Application versions use `0.2.23+pat401r1`; Docker tags use
`0.2.23-pat401r1-buildmeta1` because Docker tags cannot contain `+`.

## Merge decisions

- Regenerate Ent from the combined schema. Retain balance-card fields alongside
  the upstream 30m/12h cache counters; SQL insert/select/scan order includes
  response Codex Turn-State and its length as before.
- Regenerate Wire with both custom outbound-locale services and upstream billing
  lanes, balance notifications and digest-session shutdown hooks.
- Keep PAT/auth-PAT 401 same-account retry once. Add upstream request-scoped model
  cooldown precedence without altering the PAT retry limit.
- Keep Chat Completions reasoning-suffix aliases and use the upstream expanded
  model validation for GPT-6.1/6.2 families.
- Antigravity manual tests honor the supplied prompt with 256 output tokens;
  scheduled text tests still use the custom random arithmetic context. Image
  tests retain their image-specific prompt and generation configuration.
- Keep low-balance notifications based on cash charged after balance-card use,
  using upstream's managed notification context.

## Semantic conflict: batch billing

The upstream batch path directly deducted `users.balance`, bypassing custom
day/week/month cards. Its wallet phase now calls `deductUsageBillingWallet` for
each applied command in the same transaction. This preserves card priority,
period resets, fallback, ledger entries and per-request funding results.
Deduplication, subscription/API-key/account aggregation and atomic batch commit
remain upstream behavior. Wallet deductions are intentionally not aggregated.

The integration regression checks card exhaustion, cash fallback, ledger rows
and idempotent batch replay. Keep this test when merging future billing changes.

## Deployment constraints

Build on the local host only. Back up Compose, `.env`, application data and a
PostgreSQL custom-format dump before replacing either application container.
Deploy VPS first, verify health/data/settings/migrations/proxy bindings, then
local port 8080. Keep the local boot override pinned to the fixed image.
Do not recreate PostgreSQL/Redis or modify their mounts; preserve node proxies.
The five new SQL migrations are additive; no historical migration is rewritten.
Rollback replaces the application image only and retains the migrated database.
