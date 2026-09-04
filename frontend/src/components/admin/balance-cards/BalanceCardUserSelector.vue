<template>
  <div ref="containerRef" class="relative">
    <div v-if="selectedUserIds.length" class="mb-2 flex flex-wrap gap-2">
      <span
        v-for="userId in selectedUserIds"
        :key="userId"
        class="inline-flex max-w-full items-center gap-2 rounded-lg bg-primary-50 px-3 py-2 text-xs text-gray-700 dark:bg-primary-900/20 dark:text-gray-200"
      >
        <span class="min-w-0">
          <span class="block max-w-64 truncate font-medium" :title="selectedUserName(userId)">
            {{ selectedUserName(userId) }}
          </span>
          <span class="block max-w-64 truncate text-gray-500 dark:text-dark-400" :title="selectedUserEmail(userId)">
            {{ selectedUserEmail(userId) }}
          </span>
        </span>
        <span class="shrink-0 text-gray-400">#{{ userId }}</span>
        <button
          type="button"
          class="shrink-0 rounded text-gray-400 hover:text-red-600 dark:hover:text-red-400"
          :aria-label="t('balanceCards.admin.removeUser')"
          :title="t('balanceCards.admin.removeUser')"
          @click="removeUser(userId)"
        >
          <Icon name="x" size="xs" :stroke-width="2" />
        </button>
      </span>
    </div>

    <div class="relative">
      <Icon
        name="search"
        size="sm"
        class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
      />
      <input
        v-model="searchQuery"
        type="text"
        autocomplete="off"
        class="input w-full pl-9"
        :placeholder="t('balanceCards.admin.userSearchPlaceholder')"
        :disabled="disabled"
        role="combobox"
        :aria-expanded="showDropdown"
	      @focus="openDropdown"
	      @input="debounceSearch"
	      @keydown.escape="closeDropdown"
      />
    </div>

    <div
      v-if="showDropdown && !disabled"
	      class="absolute z-50 mt-1 max-h-64 w-full overflow-auto rounded-xl border border-gray-200 bg-white py-1 shadow-xl dark:border-dark-600 dark:bg-dark-700"
	      role="listbox"
	      @click.self="closeDropdown"
    >
      <div v-if="loading" class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400">
        {{ t('common.loading') }}
      </div>
      <div
        v-else-if="availableUsers.length === 0"
        class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400"
      >
        {{ t('balanceCards.admin.userSearchEmpty') }}
      </div>
	    <template v-else>
	      <button
	        v-for="user in availableUsers"
	        :key="user.id"
	        type="button"
	        class="flex w-full items-center justify-between gap-4 px-4 py-2.5 text-left hover:bg-gray-50 dark:hover:bg-dark-600"
	        role="option"
	        @click="selectUser(user)"
	      >
	        <span class="min-w-0">
	          <span class="block truncate text-sm font-medium text-gray-900 dark:text-white">
	            {{ user.username || t('balanceCards.admin.unnamedUser') }}
	          </span>
	          <span class="block truncate text-xs text-gray-500 dark:text-dark-400">
	            {{ user.email }}
	          </span>
	        </span>
	        <span class="shrink-0 text-xs text-gray-400">#{{ user.id }}</span>
	      </button>
	    </template>
    </div>

    <p class="mt-2 text-xs text-gray-500 dark:text-dark-400">
      {{ t('balanceCards.admin.selectedUsers', { count: selectedUserIds.length }) }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import Icon from '@/components/icons/Icon.vue'
import type { AdminUser } from '@/types'

const props = withDefaults(defineProps<{
  modelValue: number[]
  disabled?: boolean
}>(), {
  disabled: false
})

const emit = defineEmits<{
  'update:modelValue': [value: number[]]
}>()

const { t } = useI18n()
const containerRef = ref<HTMLElement | null>(null)
const searchQuery = ref('')
const searchResults = ref<AdminUser[]>([])
const selectedUsers = ref<Record<number, AdminUser>>({})
const loading = ref(false)
const showDropdown = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | null = null
let searchSequence = 0

const selectedUserIds = computed(() =>
  Array.from(new Set(props.modelValue.filter(id => Number.isInteger(id) && id > 0)))
)

const availableUsers = computed(() => {
  const selected = new Set(selectedUserIds.value)
  return searchResults.value.filter(user => !selected.has(user.id))
})

function selectedUserName(userId: number): string {
  return selectedUsers.value[userId]?.username || t('balanceCards.admin.unnamedUser')
}

function selectedUserEmail(userId: number): string {
  return selectedUsers.value[userId]?.email || t('balanceCards.admin.userIdFallback', { id: userId })
}

function cancelPendingSearch(): void {
  if (searchTimer) {
    clearTimeout(searchTimer)
    searchTimer = null
  }
  searchSequence += 1
}

async function loadUsers(query: string): Promise<void> {
  const sequence = ++searchSequence
  loading.value = true
  try {
    const result = await adminAPI.users.list(1, 30, {
      status: 'active',
      search: query || undefined,
      sort_by: 'email',
      sort_order: 'asc'
    })
    if (sequence === searchSequence) searchResults.value = result.items
  } catch {
    if (sequence === searchSequence) searchResults.value = []
  } finally {
    if (sequence === searchSequence) loading.value = false
  }
}

function openDropdown(): void {
  showDropdown.value = true
  if (!searchResults.value.length) void loadUsers(searchQuery.value.trim())
}

function closeDropdown(): void {
  cancelPendingSearch()
  loading.value = false
  showDropdown.value = false
}

function debounceSearch(): void {
  cancelPendingSearch()
  showDropdown.value = true
  const query = searchQuery.value.trim()
  searchTimer = setTimeout(() => void loadUsers(query), 300)
}

function selectUser(user: AdminUser): void {
	  cancelPendingSearch()
  selectedUsers.value = { ...selectedUsers.value, [user.id]: user }
  emit('update:modelValue', [...selectedUserIds.value, user.id])
  searchQuery.value = ''
	  searchResults.value = []
	  loading.value = false
	  showDropdown.value = false
}

function removeUser(userId: number): void {
  emit('update:modelValue', selectedUserIds.value.filter(id => id !== userId))
}

function handleDocumentClick(event: MouseEvent): void {
  const target = event.target as Node | null
	  if (target && !containerRef.value?.contains(target)) closeDropdown()
}

onMounted(() => document.addEventListener('click', handleDocumentClick, true))
onUnmounted(() => {
  cancelPendingSearch()
	  document.removeEventListener('click', handleDocumentClick, true)
})
</script>
