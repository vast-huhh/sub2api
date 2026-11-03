import { describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { updateAccountMock, checkMixedChannelRiskMock, getExitLocationsMock } = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  getExitLocationsMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: true })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { update: updateAccountMock, checkMixedChannelRisk: checkMixedChannelRiskMock },
    proxies: { getExitLocations: getExitLocationsMock },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key)
    })
  }
})

import EditAccountModal from '../EditAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: { type: [String, Number, Boolean, null], default: '' },
    options: { type: Array, default: () => [] }
  },
  emits: ['update:modelValue'],
  template: `
    <select v-bind="$attrs" :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
      <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
    </select>
  `
})

function buildOAuthAccount(extra: Record<string, unknown>) {
  return {
    id: 193,
    name: 'OpenAI OAuth',
    notes: '',
    platform: 'openai',
    type: 'oauth',
    credentials: { access_token: 'oauth-token' },
    extra,
    proxy_id: 72,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function mountModal(account: any) {
  return mount(EditAccountModal, {
    props: { show: true, account, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        Icon: true,
        ProxySelector: true,
        GroupSelector: true,
        ModelWhitelistSelector: true
      }
    }
  })
}

describe('EditAccountModal outbound timezone', () => {
  it('shows the proxy exit timezone and keeps search location default-on when saving', async () => {
    getExitLocationsMock.mockReset().mockResolvedValue([
      {
        proxy_id: 72,
        current: true,
        exit_ip: '13.113.59.232',
        country_code: 'JP',
        region: 'Tokyo',
        city: 'Tokyo',
        timezone: 'Asia/Tokyo',
        source: 'chatgpt_trace',
        succeeded_at: '2026-11-04T01:00:00Z',
        attempted_at: '2026-11-04T01:00:00Z',
        next_probe_at: '2026-11-05T01:00:00Z',
        failure_count: 0,
        last_error: ''
      }
    ])
    const account = buildOAuthAccount({ openai_outbound_timezone_mode: 'follow_proxy' })
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await flushPromises()

    expect((wrapper.get('[data-testid="edit-outbound-timezone-mode-select"]').element as HTMLSelectElement).value).toBe('follow_proxy')
    expect(wrapper.get('[data-testid="edit-outbound-timezone-status"]').text()).toContain('Asia/Tokyo')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    const extra = updateAccountMock.mock.calls[0]?.[1]?.extra
    expect(extra.openai_outbound_timezone_mode).toBe('follow_proxy')
    expect(extra).not.toHaveProperty('openai_outbound_search_location')
    expect(extra).not.toHaveProperty('openai_outbound_timezone')
  })

  it('persists an explicit search-location opt-out and clears keys when turned off', async () => {
    getExitLocationsMock.mockReset().mockResolvedValue([])
    const account = buildOAuthAccount({ openai_outbound_timezone_mode: 'follow_proxy' })
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await flushPromises()
    expect(wrapper.get('[data-testid="edit-outbound-timezone-status"]').text()).toContain('outboundTimezonePending')

    await wrapper.get('[data-testid="edit-outbound-search-location-toggle"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra.openai_outbound_search_location).toBe(false)

    await wrapper.get('[data-testid="edit-outbound-timezone-mode-select"]').setValue('off')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    const cleared = updateAccountMock.mock.calls[1]?.[1]?.extra
    expect(cleared).not.toHaveProperty('openai_outbound_timezone_mode')
    expect(cleared).not.toHaveProperty('openai_outbound_search_location')
  })

  it('saves a manual timezone', async () => {
    getExitLocationsMock.mockReset().mockResolvedValue([])
    const account = buildOAuthAccount({})
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await flushPromises()
    await wrapper.get('[data-testid="edit-outbound-timezone-mode-select"]').setValue('manual')
    await wrapper.get('[data-testid="edit-outbound-timezone-manual-input"]').setValue(' Asia/Singapore ')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    const extra = updateAccountMock.mock.calls[0]?.[1]?.extra
    expect(extra.openai_outbound_timezone_mode).toBe('manual')
    expect(extra.openai_outbound_timezone).toBe('Asia/Singapore')
  })
})
