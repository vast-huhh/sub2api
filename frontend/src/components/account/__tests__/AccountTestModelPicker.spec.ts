import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import AccountTestModal from '../AccountTestModal.vue'
import AdminAccountTestModal from '@/components/admin/account/AccountTestModal.vue'
import type { Account } from '@/types'

const { getAvailableModels } = vi.hoisted(() => ({ getAvailableModels: vi.fn() }))

vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAvailableModels } } }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

afterEach(() => {
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})

describe.each([
  ['account', AccountTestModal],
  ['admin account', AdminAccountTestModal]
] as const)('%s test model picker', (_, component) => {
  function mountPicker(type: 'oauth' | 'apikey') {
    return mount(component, {
      props: {
        show: false,
        account: { id: 42, name: 'OpenAI test', platform: 'openai', type, status: 'active', credentials: {}, extra: {} } as Account
      },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
          Teleport: true,
          Icon: true
        }
      }
    })
  }

  it.each(['oauth', 'apikey'] as const)('displays, searches and submits a live %s model by ID', async (type) => {
    // Match the picker fields supplied by FetchOpenAIAccountModels, including
    // unknown models whose missing upstream label is filled with their ID.
    const models = [
      { id: 'new-oauth-model', display_name: 'new-oauth-model', type: 'model' },
      { id: 'custom-model-id', display_name: 'Provider Model', type: 'model' },
      ...Array.from({ length: 4 }, (_, i) => ({ id: `extra-${i}`, display_name: `Extra ${i}`, type: 'model' }))
    ]
    getAvailableModels.mockResolvedValue(models)
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      body: { getReader: () => ({ read: vi.fn().mockResolvedValue({ done: true }) }) }
    })
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mountPicker(type)
    try {
      await wrapper.setProps({ show: true })
      await flushPromises()
      expect(getAvailableModels).toHaveBeenCalledWith(42)
      const picker = wrapper.findAll('button.select-trigger')[0]!
      expect(picker.text()).toBe('new-oauth-model')
      await picker.trigger('click')
      expect(wrapper.findAll('[role="option"]').map((option) => option.text())).toEqual(models.map((model) => model.display_name))
      await wrapper.get('input.select-search-input').setValue('Provider')
      expect(wrapper.findAll('[role="option"]')).toHaveLength(1)
      await wrapper.get('[role="option"]').trigger('click')
      expect(picker.text()).toBe('Provider Model')
      await wrapper.findAll('button').find((button) => button.text() === 'admin.accounts.startTest')!.trigger('click')
      await flushPromises()
      expect(fetchMock).toHaveBeenCalledTimes(1)
      expect(JSON.parse(fetchMock.mock.calls[0]![1].body).model_id).toBe('custom-model-id')
    } finally {
      wrapper.unmount()
    }
  })

  it('keeps testing disabled when the upstream catalog is genuinely empty', async () => {
    getAvailableModels.mockResolvedValue([])
    const wrapper = mountPicker('oauth')
    try {
      await wrapper.setProps({ show: true })
      await flushPromises()
      const start = wrapper.findAll('button').find((button) => button.text() === 'admin.accounts.startTest')!
      expect(start.attributes('disabled')).toBeDefined()
      await wrapper.findAll('button.select-trigger')[0]!.trigger('click')
      expect(wrapper.findAll('[role="option"]')).toHaveLength(0)
      expect(wrapper.get('.select-empty').text()).toBe('common.noOptionsFound')
    } finally {
      wrapper.unmount()
    }
  })
})
