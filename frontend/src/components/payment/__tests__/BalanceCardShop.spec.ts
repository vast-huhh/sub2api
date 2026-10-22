import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BalanceCardShop from '../BalanceCardShop.vue'
import type { BalanceCardProduct } from '@/types'

const { getProducts } = vi.hoisted(() => ({ getProducts: vi.fn() }))
vi.mock('@/api/balanceCards', () => ({ getBalanceCardProducts: getProducts }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function product(overrides: Partial<BalanceCardProduct> = {}): BalanceCardProduct {
  return {
    id: 1, name: 'Monthly card', description: 'Monthly allowance', price_cny: 20,
    purchase_url: 'https://store.example/card?id=1', card_type: 'month', validity_days: 30,
    daily_quota_usd: 10, weekly_quota_usd: 50, monthly_quota_usd: 200, max_reset_count: 20,
    ...overrides,
  }
}

describe('BalanceCardShop', () => {
  beforeEach(() => vi.resetAllMocks())

  it('opens the configured store product and shows contact instructions when no usable link exists', async () => {
    getProducts.mockResolvedValue([
      product(),
      product({ id: 2, purchase_url: '' }),
      product({ id: 3, purchase_url: 'javascript:alert(1)' }),
    ])
    const wrapper = shallowMount(BalanceCardShop)
    await flushPromises()

    expect(wrapper.findAll('article')).toHaveLength(3)
    const links = wrapper.findAll('article a')
    expect(links).toHaveLength(1)
    expect(links[0].attributes('href')).toBe('https://store.example/card?id=1')
    expect(links[0].attributes('target')).toBe('_blank')
    expect(links[0].attributes('rel')).toContain('noopener')
    expect(wrapper.findAll('article').slice(1).every(card => card.text().includes('balanceCards.shop.contactAdmin'))).toBe(true)
  })

  it('allows retrying a failed load and displays the empty catalog', async () => {
    getProducts.mockRejectedValueOnce(new Error('network unavailable')).mockResolvedValueOnce([])
    const wrapper = shallowMount(BalanceCardShop)
    await flushPromises()
    expect(wrapper.text()).toContain('balanceCards.shop.loadFailed')

    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('balanceCards.shop.empty')
    expect(wrapper.findAll('article')).toHaveLength(0)
  })
})
