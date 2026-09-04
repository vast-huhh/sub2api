import { apiClient } from '../client'
import type {
  BalanceCardLedger,
  BalanceCardPlan,
  BalanceCardPlanInput,
  PaginatedResponse,
  UserBalanceCard
} from '@/types'

export async function listPlans(): Promise<BalanceCardPlan[]> {
  const { data } = await apiClient.get<BalanceCardPlan[]>('/admin/balance-card-plans')
  return data
}

export async function createPlan(input: BalanceCardPlanInput): Promise<BalanceCardPlan> {
  const { data } = await apiClient.post<BalanceCardPlan>('/admin/balance-card-plans', input)
  return data
}

export async function updatePlan(
  id: number,
  input: BalanceCardPlanInput
): Promise<BalanceCardPlan> {
  const { data } = await apiClient.put<BalanceCardPlan>(`/admin/balance-card-plans/${id}`, input)
  return data
}

export async function listCards(
  page = 1,
  pageSize = 20,
  filters?: { user_id?: number; plan_id?: number; status?: string }
): Promise<PaginatedResponse<UserBalanceCard>> {
  const { data } = await apiClient.get<PaginatedResponse<UserBalanceCard>>('/admin/balance-cards', {
    params: { page, page_size: pageSize, ...filters }
  })
  return data
}

export async function assign(input: {
  user_id: number
  plan_id: number
  notes?: string
}): Promise<UserBalanceCard> {
  const { data } = await apiClient.post<UserBalanceCard>('/admin/balance-cards/assign', input)
  return data
}

export async function bulkAssign(input: {
  user_ids: number[]
  plan_id: number
  notes?: string
}): Promise<UserBalanceCard[]> {
  const { data } = await apiClient.post<UserBalanceCard[]>('/admin/balance-cards/bulk-assign', input)
  return data
}

export async function extend(id: number, days: number): Promise<UserBalanceCard> {
  const { data } = await apiClient.post<UserBalanceCard>(`/admin/balance-cards/${id}/extend`, { days })
  return data
}

export async function resetDaily(id: number): Promise<UserBalanceCard> {
  const operationKey = `admin-balance-card-reset:${id}:${crypto.randomUUID()}`
  const { data } = await apiClient.post<UserBalanceCard>(
    `/admin/balance-cards/${id}/reset-daily`,
    { operation_key: operationKey },
    { headers: { 'Idempotency-Key': operationKey } }
  )
  return data
}

export async function revoke(id: number): Promise<UserBalanceCard> {
  const { data } = await apiClient.post<UserBalanceCard>(`/admin/balance-cards/${id}/revoke`)
  return data
}

export async function deleteCard(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/balance-cards/${id}`)
  return data
}

export async function ledger(
  id: number,
  page = 1,
  pageSize = 20
): Promise<PaginatedResponse<BalanceCardLedger>> {
  const { data } = await apiClient.get<PaginatedResponse<BalanceCardLedger>>(
    `/admin/balance-cards/${id}/ledger`,
    { params: { page, page_size: pageSize } }
  )
  return data
}

export default {
  listPlans,
  createPlan,
  updatePlan,
  listCards,
  assign,
  bulkAssign,
  extend,
  resetDaily,
  revoke,
  delete: deleteCard,
  ledger
}
