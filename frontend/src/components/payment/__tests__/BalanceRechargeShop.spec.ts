import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BalanceRechargeShop from '../BalanceRechargeShop.vue'
import type { BalanceRechargeProduct } from '@/api/balanceRecharge'

const { getProducts } = vi.hoisted(() => ({ getProducts: vi.fn() }))
vi.mock('@/api/balanceRecharge', () => ({ getBalanceRechargeProducts: getProducts }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function product(overrides: Partial<BalanceRechargeProduct> = {}): BalanceRechargeProduct {
  return { id: '1', name: 'Balance recharge', description: '', price_cny: 1, credit_usd: 5, purchase_url: 'https://store.example/balance?id=1', for_sale: true, sort_order: 0, ...overrides }
}

describe('BalanceRechargeShop', () => {
  beforeEach(() => vi.resetAllMocks())

  it('opens the configured store link and uses the administrator notice for missing or invalid links', async () => {
    getProducts.mockResolvedValue([
      product(), product({ id: '2', purchase_url: '' }), product({ id: '3', purchase_url: 'javascript:alert(1)' })
    ])
    const wrapper = shallowMount(BalanceRechargeShop, { props: { showEmpty: true } })
    await flushPromises()
    const links = wrapper.findAll('a')
    expect(links).toHaveLength(1)
    expect(links[0].attributes('href')).toBe('https://store.example/balance?id=1')
    expect(links[0].attributes('target')).toBe('_blank')
    expect(links[0].attributes('rel')).toContain('noopener')
    expect(wrapper.findAll('article').slice(1).every(card => card.text().includes('rechargeProducts.contactAdmin'))).toBe(true)
    expect(wrapper.text()).toContain('$5.00')
  })

  it('retries a failed request and hides an empty catalog when online payment is available', async () => {
    getProducts.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce([])
    const wrapper = shallowMount(BalanceRechargeShop, { props: { showEmpty: true } })
    await flushPromises()
    expect(wrapper.text()).toContain('rechargeProducts.loadFailed')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('rechargeProducts.emptyShop')
    await wrapper.setProps({ showEmpty: false })
    expect(wrapper.find('section').exists()).toBe(false)
  })
})
