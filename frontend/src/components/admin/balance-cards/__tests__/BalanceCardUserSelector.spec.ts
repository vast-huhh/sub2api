import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import BalanceCardUserSelector from '../BalanceCardUserSelector.vue'

const listUsers = vi.fn()

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      list: (...args: unknown[]) => listUsers(...args)
    }
  }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      if (key === 'balanceCards.admin.selectedUsers') return `${params?.count ?? 0} selected`
      if (key === 'balanceCards.admin.unnamedUser') return 'No username'
      if (key === 'balanceCards.admin.userIdFallback') return `User #${params?.id}`
      return key
    }
  })
}))

function user(id: number, username: string, email: string) {
  return { id, username, email, status: 'active' }
}

describe('BalanceCardUserSelector', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    listUsers.mockReset()
    listUsers.mockResolvedValue({
      items: [
        user(7, 'alice', 'alice@example.com'),
        user(8, 'bob', 'bob@example.com')
      ],
      page: 1,
      page_size: 30,
      total: 2,
      pages: 1
    })
  })

  afterEach(() => vi.useRealTimers())

  it('lists active users and displays username, email, and ID', async () => {
    const wrapper = mount(BalanceCardUserSelector, {
      props: { modelValue: [] },
      global: { stubs: { Icon: true } }
    })

    await wrapper.get('input').trigger('focus')
    await flushPromises()

    expect(listUsers).toHaveBeenCalledWith(1, 30, {
      status: 'active',
      search: undefined,
      sort_by: 'email',
      sort_order: 'asc'
    })
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.text()).toContain('alice@example.com')
    expect(wrapper.text()).toContain('#7')
  })

  it('searches remotely by username or email and supports multiple selections', async () => {
    const wrapper = mount(BalanceCardUserSelector, {
      props: { modelValue: [] },
      global: { stubs: { Icon: true } }
    })
    const input = wrapper.get('input')
    await input.trigger('focus')
    await flushPromises()

    await input.setValue('alice')
    await input.trigger('input')
    vi.advanceTimersByTime(300)
    await flushPromises()
    expect(listUsers).toHaveBeenLastCalledWith(1, 30, {
      status: 'active',
      search: 'alice',
      sort_by: 'email',
      sort_order: 'asc'
    })

    const alice = wrapper.findAll('[role="option"]').find(option => option.text().includes('alice@example.com'))
    expect(alice).toBeDefined()
	    await alice!.trigger('click')
	    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[7]])
	    expect(wrapper.find('[role="listbox"]').exists()).toBe(false)

	    await wrapper.setProps({ modelValue: [7] })
	    await input.trigger('focus')
	    await flushPromises()
    const bob = wrapper.findAll('[role="option"]').find(option => option.text().includes('bob@example.com'))
    expect(bob).toBeDefined()
	    await bob!.trigger('click')
	    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[7, 8]])
	    expect(wrapper.find('[role="listbox"]').exists()).toBe(false)
	  })

	  it('closes when clicking outside even if the dialog stops click bubbling', async () => {
	    const wrapper = mount(BalanceCardUserSelector, {
	      attachTo: document.body,
	      props: { modelValue: [] },
	      global: { stubs: { Icon: true } }
	    })
	    const dialogBlank = document.createElement('button')
	    dialogBlank.addEventListener('click', event => event.stopPropagation())
	    document.body.appendChild(dialogBlank)

	    await wrapper.get('input').trigger('focus')
	    await flushPromises()
	    expect(wrapper.find('[role="listbox"]').exists()).toBe(true)

	    dialogBlank.click()
	    await wrapper.vm.$nextTick()
	    expect(wrapper.find('[role="listbox"]').exists()).toBe(false)

	    wrapper.unmount()
	    dialogBlank.remove()
	  })
})
