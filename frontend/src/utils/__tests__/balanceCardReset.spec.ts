import { describe, expect, it } from 'vitest'

import type { UserBalanceCard } from '@/types'
import {
  balanceCardResetCostDays,
  balanceCardResetWindow,
  weeklyResetDurationMs,
} from '@/utils/balanceCardReset'

const DAY_MS = 24 * 60 * 60 * 1000
const nowMs = Date.UTC(2026, 8, 3, 12)

function monthCard(overrides: Partial<UserBalanceCard> = {}): UserBalanceCard {
  return {
    status: 'active',
    card_type: 'month',
    daily_quota_usd: 60,
    daily_usage_usd: 40,
    weekly_quota_usd: 300,
    weekly_usage_usd: 300,
    weekly_window_start: new Date(nowMs - 5 * DAY_MS).toISOString(),
    monthly_quota_usd: 1000,
    monthly_usage_usd: 300,
    reset_count: 0,
    max_reset_count: 20,
    expires_at: new Date(nowMs + 20 * DAY_MS).toISOString(),
    ...overrides,
  } as UserBalanceCard
}

describe('balance-card advance reset', () => {
  it('credits four daily advances against the remaining week', () => {
    const card = monthCard({
      weekly_window_start: new Date(nowMs - DAY_MS).toISOString(),
      weekly_daily_advance_seconds: 4 * 86400,
      expires_at: new Date(nowMs + 3 * DAY_MS).toISOString(),
    })
    expect(weeklyResetDurationMs(card, nowMs)).toBe(2 * DAY_MS)
    expect(balanceCardResetWindow(card, nowMs)).toBe('weekly')
    expect(balanceCardResetCostDays(card, 'weekly', nowMs)).toBe(2)
  })

  it('allows a zero-cost weekly reset but still enforces expiry and reset count', () => {
    const card = monthCard({ weekly_daily_advance_seconds: 4 * 86400 })
    expect(weeklyResetDurationMs(card, nowMs)).toBe(0)
    expect(balanceCardResetCostDays(card, 'weekly', nowMs)).toBe(0)
    expect(balanceCardResetWindow(card, nowMs)).toBe('weekly')
    expect(balanceCardResetWindow({ ...card, expires_at: new Date(nowMs).toISOString() }, nowMs)).toBeNull()
    expect(balanceCardResetWindow({ ...card, reset_count: 20 }, nowMs)).toBeNull()
  })

  it('starts a new month-card week and consumes only the current week remainder', () => {
    const card = monthCard()

    expect(balanceCardResetWindow(card, nowMs)).toBe('weekly')
    expect(weeklyResetDurationMs(card, nowMs)).toBe(2 * DAY_MS)
    expect(balanceCardResetCostDays(card, 'weekly', nowMs)).toBe(2)
  })

  it('does not reset a week-card total or an exhausted month-card total', () => {
    expect(balanceCardResetWindow(monthCard({ card_type: 'week' }), nowMs)).toBeNull()
    expect(balanceCardResetWindow(monthCard({ monthly_usage_usd: 1000 }), nowMs)).toBeNull()
  })

  it('keeps the one-day reset for a daily limit exhausted inside an open week', () => {
    const card = monthCard({ daily_usage_usd: 60, weekly_usage_usd: 200 })

    expect(balanceCardResetWindow(card, nowMs)).toBe('daily')
    expect(balanceCardResetCostDays(card, 'daily', nowMs)).toBe(1)
  })
})
