import { apiClient } from './client'
import type { BalanceCardLedger, PaginatedResponse, UserBalanceCard } from '@/types'

export async function getMyBalanceCards(): Promise<UserBalanceCard[]> {
  const { data } = await apiClient.get<UserBalanceCard[]>('/balance-cards')
  return data
}

export async function updateBalanceCardPreferences(
  id: number,
  preferences: { fallback_enabled?: boolean; auto_reset_enabled?: boolean }
): Promise<UserBalanceCard> {
  const { data } = await apiClient.patch<UserBalanceCard>(
    `/balance-cards/${id}/preferences`,
    preferences
  )
  return data
}

export async function resetBalanceCardDaily(id: number): Promise<UserBalanceCard> {
  const operationKey = `balance-card-reset:${id}:${crypto.randomUUID()}`
  const { data } = await apiClient.post<UserBalanceCard>(
    `/balance-cards/${id}/reset-daily`,
    { operation_key: operationKey },
    { headers: { 'Idempotency-Key': operationKey } }
  )
  return data
}

export async function getBalanceCardLedger(
  id: number,
  page = 1,
  pageSize = 20
): Promise<PaginatedResponse<BalanceCardLedger>> {
  const { data } = await apiClient.get<PaginatedResponse<BalanceCardLedger>>(
    `/balance-cards/${id}/ledger`,
    { params: { page, page_size: pageSize } }
  )
  return data
}

export default {
  getMyBalanceCards,
  updateBalanceCardPreferences,
  resetBalanceCardDaily,
  getBalanceCardLedger
}
