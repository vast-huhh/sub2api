import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BalanceRechargeProductsView from '../BalanceRechargeProductsView.vue'
import type { BalanceRechargeCatalog, BalanceRechargeProduct } from '@/api/balanceRecharge'

const { getCatalog, updateCatalog, showError } = vi.hoisted(() => ({ getCatalog: vi.fn(), updateCatalog: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/balanceRecharge', () => ({ getBalanceRechargeCatalog: getCatalog, updateBalanceRechargeCatalog: updateCatalog }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const initialProduct: BalanceRechargeProduct = { id: 'default', name: '余额充值永不过期', description: '', price_cny: 1, credit_usd: 5, purchase_url: '', for_sale: true, sort_order: 0 }
const stubs = {
  AppLayout: defineComponent({ template: '<main><slot /></main>' }),
  BaseDialog: defineComponent({ props: { show: Boolean }, template: '<div v-if="show"><slot /></div>' }),
  RouterLink: true,
  Icon: true
}

describe('BalanceRechargeProductsView', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    getCatalog.mockResolvedValue({ enabled: true, products: [{ ...initialProduct }] })
    updateCatalog.mockImplementation(async (catalog: BalanceRechargeCatalog) => ({ ...catalog, products: catalog.products.map((item, i) => ({ ...item, id: item.id || `new-${i}` })) }))
  })

  it('saves a store link, unlists products and deletes them without restoring defaults', async () => {
    const wrapper = mount(BalanceRechargeProductsView, { global: { stubs } })
    await flushPromises()
    const button = (text: string) => wrapper.findAll('button').find(item => item.text() === text)!
    await button('common.edit').trigger('click')
    await wrapper.get('input[type="url"]').setValue('https://store.example/balance?id=1')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateCatalog).toHaveBeenLastCalledWith({ enabled: true, products: [{ ...initialProduct, purchase_url: 'https://store.example/balance?id=1' }] })

    await button('rechargeProducts.unlist').trigger('click')
    await flushPromises()
    expect(updateCatalog.mock.lastCall![0].products[0].for_sale).toBe(false)
    expect(wrapper.text()).toContain('rechargeProducts.offSale')

    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await button('common.delete').trigger('click')
    await flushPromises()
    expect(updateCatalog).toHaveBeenLastCalledWith({ enabled: true, products: [] })
    expect(wrapper.text()).toContain('rechargeProducts.emptyAdmin')
  })

  it('creates a priced product and preserves the displayed switch state when saving fails', async () => {
    const wrapper = mount(BalanceRechargeProductsView, { global: { stubs } })
    await flushPromises()
    await wrapper.findAll('button').find(item => item.text() === 'rechargeProducts.create')!.trigger('click')
    const inputFor = (key: string) => wrapper.findAll('label').find(item => item.text() === key)!.get('input')
    await inputFor('common.name').setValue('20 元充值')
    await inputFor('rechargeProducts.priceLabel').setValue('20')
    await inputFor('rechargeProducts.creditLabel').setValue('100')
    await inputFor('rechargeProducts.purchaseURL').setValue('https://store.example/balance?id=20')
    await inputFor('rechargeProducts.forSale').setValue(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateCatalog.mock.lastCall![0].products[1]).toEqual(expect.objectContaining({ name: '20 元充值', price_cny: 20, credit_usd: 100, for_sale: true }))
    expect(wrapper.findAll('article')).toHaveLength(2)

    updateCatalog.mockRejectedValueOnce(new Error('save failed'))
    await wrapper.get('#recharge-products-enabled').setValue(false)
    await flushPromises()
    expect((wrapper.get('#recharge-products-enabled').element as HTMLInputElement).checked).toBe(true)
    expect(showError).toHaveBeenCalledWith('save failed')
  })
})
