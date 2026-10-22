import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import BalanceCardsView from '@/views/admin/BalanceCardsView.vue'

const { listPlans, listCards, deleteCard, deletePlan, createPlan, updatePlan, showSuccess, showError } = vi.hoisted(() => ({
  listPlans: vi.fn(),
  listCards: vi.fn(),
  deleteCard: vi.fn(),
  deletePlan: vi.fn(),
  createPlan: vi.fn(),
  updatePlan: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    balanceCards: {
      listPlans,
      listCards,
      delete: deleteCard,
      deletePlan,
      assign: vi.fn(),
      bulkAssign: vi.fn(),
      createPlan,
      updatePlan,
      extend: vi.fn(),
      resetDaily: vi.fn(),
      revoke: vi.fn(),
      ledger: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError, showWarning: vi.fn() })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const AppLayoutStub = defineComponent({ template: '<main><slot /></main>' })
const BaseDialogStub = defineComponent({
  props: { show: Boolean },
  template: '<div v-if="show"><slot /></div>'
})

function card(id: number, status: 'active' | 'revoked' | 'expired' | 'pending' | 'suspended') {
  return {
    id,
    user_id: 1,
    user_email: 'user@example.com',
    plan_id: 1,
    plan_name: 'Month card',
    card_type: 'month',
    validity_days: 30,
    daily_quota_usd: 60,
    weekly_quota_usd: 0,
    monthly_quota_usd: 1000,
    max_reset_count: 20,
    starts_at: '2026-09-01T00:00:00Z',
    expires_at: '2026-10-01T00:00:00Z',
    status,
    daily_window_start: null,
    daily_usage_usd: 0,
    weekly_window_start: null,
    weekly_usage_usd: 0,
    monthly_usage_usd: 0,
    fallback_enabled: true,
    auto_reset_enabled: false,
    reset_count: 0,
    assigned_by: 1,
    assigned_at: '2026-09-01T00:00:00Z',
    activated_at: null,
    notes: '',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z'
  }
}

function page(items: ReturnType<typeof card>[]) {
  return { items, page: 1, page_size: 20, total: items.length, pages: 1 }
}

const plan = {
  id: 1, name: 'Monthly plan', description: '', card_type: 'month', validity_days: 30,
  daily_quota_usd: 10, weekly_quota_usd: 50, monthly_quota_usd: 200,
  fallback_default: true, auto_reset_default: false, max_reset_count: 20,
  status: 'active', sort_order: 0, price_cny: 20, for_sale: true, purchase_url: ''
}

function mountView() {
  return mount(BalanceCardsView, {
    global: { stubs: {
      AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub,
      BalanceCardUserSelector: true, Icon: true, RouterLink: true
    } }
  })
}

describe('BalanceCardsView delete action', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    listPlans.mockResolvedValue([])
    listCards.mockResolvedValue(page([card(2, 'active'), card(1, 'revoked')]))
    deleteCard.mockResolvedValue({ message: 'ok' })
    deletePlan.mockResolvedValue({ message: 'ok' })
  })

  it('offers deletion for expired and revoked cards and refreshes after deletion', async () => {
    listCards.mockResolvedValue(page([
      card(5, 'pending'), card(4, 'suspended'), card(3, 'expired'), card(2, 'active'), card(1, 'revoked')
    ]))
    const wrapper = mount(BalanceCardsView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          BaseDialog: BaseDialogStub,
          BalanceCardUserSelector: true,
          Icon: true
        }
      }
    })
    await flushPromises()

    const deleteButtons = wrapper.findAll('button').filter(button => button.text() === 'common.delete')
    expect(deleteButtons).toHaveLength(2)

    listCards.mockResolvedValueOnce(page([card(2, 'active'), card(1, 'revoked')]))
    await deleteButtons[0].trigger('click')
    await flushPromises()

    expect(window.confirm).toHaveBeenCalledWith('balanceCards.admin.deleteConfirm')
    expect(deleteCard).toHaveBeenCalledWith(3)
    expect(listCards).toHaveBeenCalledTimes(2)
    expect(showSuccess).toHaveBeenCalledWith('balanceCards.admin.deleteSuccess')
  })

  it('does not delete a card when confirmation is cancelled', async () => {
    vi.mocked(window.confirm).mockReturnValue(false)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'common.delete')!.trigger('click')
    expect(deleteCard).not.toHaveBeenCalled()
  })

  it('returns to the previous page after deleting its last card', async () => {
    listCards.mockResolvedValue({ ...page([card(1, 'expired')]), page: 2, pages: 2, total: 21 })
    const wrapper = mountView()
    await flushPromises()
    listCards.mockResolvedValueOnce({ ...page([]), page: 2, pages: 1, total: 20 })
      .mockResolvedValueOnce(page([card(2, 'active')]))
    await wrapper.findAll('button').find(button => button.text() === 'common.delete')!.trigger('click')
    await flushPromises()
    expect(listCards).toHaveBeenLastCalledWith(1, 20, expect.any(Object))
    expect(wrapper.text()).toContain('#2')
  })

  it('deletes a plan, clears its selected filter and keeps issued cards visible', async () => {
    listPlans.mockResolvedValue([plan])
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('select').setValue('1')
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'balanceCards.admin.plans')!.trigger('click')
    listPlans.mockResolvedValueOnce([])
    await wrapper.get('article').findAll('button').find(button => button.text() === 'common.delete')!.trigger('click')
    await flushPromises()
    expect(window.confirm).toHaveBeenCalledWith('balanceCards.admin.deletePlanConfirm')
    expect(deletePlan).toHaveBeenCalledWith(1)
    expect(wrapper.findAll('article')).toHaveLength(0)
    expect(wrapper.text()).toContain('common.noData')
    expect(listCards).toHaveBeenLastCalledWith(1, 20, expect.objectContaining({ plan_id: undefined }))
    expect(showSuccess).toHaveBeenCalledWith('balanceCards.admin.deletePlanSuccess')
    await wrapper.findAll('button').find(button => button.text() === 'balanceCards.admin.issuedCards')!.trigger('click')
    expect(wrapper.text()).toContain('#2')
  })

  it('keeps a plan when deletion is cancelled or fails', async () => {
    listPlans.mockResolvedValue([plan])
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'balanceCards.admin.plans')!.trigger('click')
    const button = wrapper.get('article').findAll('button').find(button => button.text() === 'common.delete')!
    vi.mocked(window.confirm).mockReturnValue(false)
    await button.trigger('click')
    expect(deletePlan).not.toHaveBeenCalled()
    vi.mocked(window.confirm).mockReturnValue(true)
    deletePlan.mockRejectedValueOnce(new Error('Delete failed'))
    await button.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(button.attributes('disabled')).toBeUndefined()
    expect(showError).toHaveBeenCalledWith('Delete failed')
  })

  it('saves store settings and allows unlisting without clearing the price or store link', async () => {
    const wrapper = mount(BalanceCardsView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub, BaseDialog: BaseDialogStub,
          BalanceCardUserSelector: true, Icon: true, RouterLink: true
        }
      }
    })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'balanceCards.admin.plans')!.trigger('click')
    await wrapper.findAll('button').find(button => button.text() === 'balanceCards.admin.createPlan')!.trigger('click')

    const inputFor = (label: string) => wrapper.findAll('label').find(item => item.text() === label)!.get('input')
    await inputFor('common.name').setValue('Monthly shop card')
    await inputFor('balanceCards.admin.priceCNY').setValue('22.50')
    await inputFor('balanceCards.admin.purchaseURL').setValue('https://store.example/card?id=3')
    await inputFor('balanceCards.admin.forSale').setValue(true)
    const saved = {
      id: 3, name: 'Monthly shop card', description: '', price_cny: 22.5, for_sale: true,
      purchase_url: 'https://store.example/card?id=3', card_type: 'month', validity_days: 30,
      daily_quota_usd: 10, weekly_quota_usd: 50, monthly_quota_usd: 200,
      fallback_default: true, auto_reset_default: false, max_reset_count: 20, status: 'active', sort_order: 0
    }
    listPlans.mockResolvedValue([saved])
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(createPlan).toHaveBeenCalledWith(expect.objectContaining({
      price_cny: 22.5, for_sale: true, purchase_url: 'https://store.example/card?id=3'
    }))
    expect(wrapper.text()).toContain('balanceCards.admin.onSale')

    await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
    await inputFor('balanceCards.admin.forSale').setValue(false)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updatePlan).toHaveBeenCalledWith(3, expect.objectContaining({
      price_cny: 22.5, for_sale: false, purchase_url: 'https://store.example/card?id=3'
    }))
    expect(showError).not.toHaveBeenCalled()
  })
})
