import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import BalanceCardsView from '@/views/admin/BalanceCardsView.vue'

const { listPlans, listCards, deleteCard, showSuccess, showError } = vi.hoisted(() => ({
  listPlans: vi.fn(),
  listCards: vi.fn(),
  deleteCard: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    balanceCards: {
      listPlans,
      listCards,
      delete: deleteCard,
      assign: vi.fn(),
      bulkAssign: vi.fn(),
      createPlan: vi.fn(),
      updatePlan: vi.fn(),
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

function card(id: number, status: 'active' | 'revoked') {
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

describe('BalanceCardsView delete action', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    listPlans.mockResolvedValue([])
    listCards.mockResolvedValue(page([card(2, 'active'), card(1, 'revoked')]))
    deleteCard.mockResolvedValue({ message: 'ok' })
  })

  it('only offers deletion for revoked cards and refreshes both lists after deletion', async () => {
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
    expect(deleteButtons).toHaveLength(1)

    listCards.mockResolvedValueOnce(page([card(2, 'active')]))
    await deleteButtons[0].trigger('click')
    await flushPromises()

    expect(window.confirm).toHaveBeenCalledWith('balanceCards.admin.deleteConfirm')
    expect(deleteCard).toHaveBeenCalledWith(1)
    expect(listCards).toHaveBeenCalledTimes(2)
    expect(showSuccess).toHaveBeenCalledWith('balanceCards.admin.deleteSuccess')
  })
})
