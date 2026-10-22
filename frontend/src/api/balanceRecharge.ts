import { apiClient } from './client'

export interface BalanceRechargeProduct {
  id: string
  name: string
  description: string
  price_cny: number
  credit_usd: number
  purchase_url: string
  for_sale: boolean
  sort_order: number
}

export interface BalanceRechargeCatalog {
  enabled: boolean
  products: BalanceRechargeProduct[]
}

export async function getBalanceRechargeProducts(): Promise<BalanceRechargeProduct[]> {
  const { data } = await apiClient.get<BalanceRechargeProduct[]>('/payment/recharge-products')
  return data
}

export async function getBalanceRechargeCatalog(): Promise<BalanceRechargeCatalog> {
  const { data } = await apiClient.get<BalanceRechargeCatalog>('/admin/payment/recharge-products')
  return data
}

export async function updateBalanceRechargeCatalog(catalog: BalanceRechargeCatalog): Promise<BalanceRechargeCatalog> {
  const { data } = await apiClient.put<BalanceRechargeCatalog>('/admin/payment/recharge-products', catalog)
  return data
}
