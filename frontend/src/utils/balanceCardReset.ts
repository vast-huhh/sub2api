import type { UserBalanceCard } from '@/types'

export type BalanceCardResetWindow = 'daily' | 'weekly'

const DAY_MS = 24 * 60 * 60 * 1000

function remaining(limit: number, usage: number): number {
  return limit > 0 ? Math.max(0, limit - usage) : Number.POSITIVE_INFINITY
}

export function weeklyResetDurationMs(card: UserBalanceCard, nowMs = Date.now()): number {
  if (!card.weekly_window_start) return 7 * DAY_MS
  return Math.max(0, new Date(card.weekly_window_start).getTime() + 7 * DAY_MS - nowMs)
}

export function balanceCardResetWindow(
  card: UserBalanceCard,
  nowMs = Date.now()
): BalanceCardResetWindow | null {
  if (card.status !== 'active' || card.reset_count >= card.max_reset_count) return null

  const expiresAtMs = new Date(card.expires_at).getTime()
  const monthlyRemaining = remaining(card.monthly_quota_usd, card.monthly_usage_usd)
  const weeklyRemaining = remaining(card.weekly_quota_usd, card.weekly_usage_usd)

  if (
    card.card_type === 'month' &&
    card.weekly_quota_usd > 0 &&
    weeklyRemaining <= 0 &&
    Math.min(remaining(card.daily_quota_usd, 0), card.weekly_quota_usd, monthlyRemaining) > 0
  ) {
    const resetDuration = weeklyResetDurationMs(card, nowMs)
    if (resetDuration > 0 && expiresAtMs > nowMs + resetDuration) return 'weekly'
  }

  const dailyRemaining = remaining(card.daily_quota_usd, card.daily_usage_usd)
  if (
    card.daily_quota_usd > 0 &&
    dailyRemaining <= 0 &&
    Math.min(card.daily_quota_usd, weeklyRemaining, monthlyRemaining) > 0 &&
    expiresAtMs > nowMs + DAY_MS
  ) {
    return 'daily'
  }

  return null
}

export function balanceCardResetCostDays(
  card: UserBalanceCard,
  window: BalanceCardResetWindow,
  nowMs = Date.now()
): number {
  if (window === 'daily') return 1
  return Math.max(1, Math.ceil(weeklyResetDurationMs(card, nowMs) / DAY_MS))
}
