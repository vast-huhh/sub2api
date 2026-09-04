<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" class="flex justify-center py-12">
        <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
      </div>

      <div v-else-if="cards.length === 0" class="card p-12 text-center">
        <Icon name="creditCard" size="xl" class="mx-auto mb-4 text-gray-400" />
        <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('balanceCards.empty') }}</h3>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ t('balanceCards.emptyHint') }}</p>
      </div>

      <div v-else class="grid gap-6 lg:grid-cols-2">
        <section
          v-for="card in cards"
          :key="card.id"
          class="overflow-hidden rounded-2xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800"
        >
          <header class="flex items-start justify-between border-b border-gray-100 p-5 dark:border-dark-700">
            <div>
              <div class="flex items-center gap-2">
                <span class="h-2 w-2 rounded-full" :class="statusDot(card.status)" />
                <h3 class="font-semibold text-gray-900 dark:text-white">{{ card.plan_name }}</h3>
              </div>
              <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                {{ t(`balanceCards.types.${card.card_type}`) }} · {{ card.validity_days }} {{ t('balanceCards.days') }}
              </p>
            </div>
            <span class="rounded-full px-2.5 py-1 text-xs font-medium" :class="statusBadge(card.status)">
              {{ t(`balanceCards.status.${card.status}`) }}
            </span>
          </header>

          <div class="space-y-5 p-5">
            <div class="flex items-center justify-between text-sm">
              <span class="text-gray-500 dark:text-dark-400">{{ t('balanceCards.expiresAt') }}</span>
              <span class="font-medium text-gray-800 dark:text-gray-200">{{ formatDateTime(card.expires_at) }}</span>
            </div>

            <div v-for="quota in cardQuotaRows(card)" :key="quota.key">
              <div class="mb-2 flex items-center justify-between text-sm">
                <span class="font-medium text-gray-700 dark:text-gray-300">{{ quota.label }}</span>
	                <span v-if="quota.limit > 0" class="text-gray-500 dark:text-dark-400">
	                  ${{ quota.usage.toFixed(2) }} / ${{ quota.limit.toFixed(2) }}
	                </span>
	                <span v-else class="text-emerald-600 dark:text-emerald-400">{{ t('balanceCards.unlimited') }}</span>
              </div>
	              <div v-if="quota.limit > 0" class="h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                <div
                  class="h-full rounded-full transition-all"
                  :class="quotaPercent(quota.usage, quota.limit) >= 100 ? 'bg-red-500' : 'bg-primary-500'"
                  :style="{ width: `${quotaPercent(quota.usage, quota.limit)}%` }"
                />
              </div>
	              <p v-if="quota.limit > 0" class="mt-2 text-xs text-gray-500 dark:text-dark-400">
                {{ t('balanceCards.quotaRemaining', { amount: quotaRemaining(quota.usage, quota.limit).toFixed(2) }) }}
              </p>
	              <p v-else class="mt-2 text-xs text-gray-500 dark:text-dark-400">{{ t('balanceCards.quotaUnlimited') }}</p>
            </div>

            <label class="flex items-center justify-between gap-4 rounded-xl bg-gray-50 p-3 dark:bg-dark-700/60">
              <span>
                <span class="block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('balanceCards.fallback') }}</span>
                <span class="block text-xs text-gray-500 dark:text-dark-400">{{ t('balanceCards.fallbackHint') }}</span>
              </span>
              <input
                type="checkbox"
                class="h-5 w-5 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
	                :checked="card.fallback_enabled"
	                :disabled="savingCardId === card.id || card.status !== 'active'"
                @change="togglePreference(card, 'fallback_enabled', $event)"
              />
            </label>

            <label class="flex items-center justify-between gap-4 rounded-xl bg-gray-50 p-3 dark:bg-dark-700/60">
              <span>
                <span class="block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('balanceCards.autoReset') }}</span>
                <span class="block text-xs text-gray-500 dark:text-dark-400">{{ t('balanceCards.autoResetHint') }}</span>
              </span>
              <input
                type="checkbox"
                class="h-5 w-5 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
	                :checked="card.auto_reset_enabled"
	                :disabled="savingCardId === card.id || card.status !== 'active' || (card.daily_quota_usd <= 0 && !(card.card_type === 'month' && card.weekly_quota_usd > 0))"
                @change="togglePreference(card, 'auto_reset_enabled', $event)"
              />
            </label>

            <div class="flex items-center justify-between gap-3 border-t border-gray-100 pt-4 dark:border-dark-700">
              <span class="text-xs text-gray-500 dark:text-dark-400">
                {{ t('balanceCards.resetCount', { current: card.reset_count, max: card.max_reset_count }) }}
              </span>
              <div class="flex gap-2">
                <button class="btn btn-secondary px-3 py-1.5 text-xs" @click="openLedger(card)">
                  {{ t('balanceCards.ledger') }}
                </button>
                <button
                  class="btn btn-primary px-3 py-1.5 text-xs"
                  :disabled="!canReset(card) || resettingCardId === card.id"
                  @click="resetQuota(card)"
                >
                  {{ resettingCardId === card.id ? t('common.processing') : resetButtonLabel(card) }}
                </button>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>

    <BaseDialog :show="showLedger" :title="t('balanceCards.ledger')" width="wide" @close="showLedger = false">
      <div v-if="ledgerLoading" class="py-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
      <div v-else class="overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead><tr class="text-left text-xs text-gray-500"><th class="px-3 py-2">{{ t('balanceCards.event') }}</th><th class="px-3 py-2">{{ t('balanceCards.amount') }}</th><th class="px-3 py-2">{{ t('balanceCards.usageAfter') }}</th><th class="px-3 py-2">{{ t('balanceCards.time') }}</th></tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="item in ledger" :key="item.id"><td class="px-3 py-3">{{ t(`balanceCards.events.${item.event_type}`, item.event_type) }}</td><td class="px-3 py-3">${{ item.amount_usd.toFixed(4) }}</td><td class="px-3 py-3">${{ item.daily_usage_after.toFixed(4) }}</td><td class="px-3 py-3 whitespace-nowrap">{{ formatDateTime(item.created_at) }}</td></tr>
            <tr v-if="ledger.length === 0"><td colspan="4" class="px-3 py-8 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
          </tbody>
        </table>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import balanceCardsAPI from '@/api/balanceCards'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { balanceCardResetCostDays, balanceCardResetWindow } from '@/utils/balanceCardReset'
import type { BalanceCardLedger, UserBalanceCard } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const cards = ref<UserBalanceCard[]>([])
const loading = ref(false)
const savingCardId = ref<number | null>(null)
const resettingCardId = ref<number | null>(null)
const showLedger = ref(false)
const ledgerLoading = ref(false)
const ledger = ref<BalanceCardLedger[]>([])

function quotaPercent(usage: number, limit: number): number {
  return limit > 0 ? Math.min(100, Math.max(0, (usage / limit) * 100)) : 0
}

function quotaRemaining(usage: number, limit: number): number {
  return Math.max(0, limit - usage)
}

function cardQuotaRows(card: UserBalanceCard) {
  const rows = [{ key: 'daily', label: t('balanceCards.todayQuota'), usage: card.daily_usage_usd, limit: card.daily_quota_usd }]
	  if (card.card_type === 'week' || card.card_type === 'month') rows.push({ key: 'weekly', label: card.card_type === 'week' ? t('balanceCards.weekCardTotal') : t('balanceCards.weekQuota'), usage: card.weekly_usage_usd, limit: card.weekly_quota_usd })
	  if (card.card_type === 'month') rows.push({ key: 'monthly', label: t('balanceCards.monthCardTotal'), usage: card.monthly_usage_usd, limit: card.monthly_quota_usd })
  return rows
}

function canReset(card: UserBalanceCard): boolean {
	  return balanceCardResetWindow(card) !== null
}

function resetButtonLabel(card: UserBalanceCard): string {
  return balanceCardResetWindow(card) === 'weekly' ? t('balanceCards.resetWeek') : t('balanceCards.resetToday')
}

function statusDot(status: string): string {
  return status === 'active' ? 'bg-emerald-500' : status === 'pending' ? 'bg-amber-500' : 'bg-gray-400'
}

function statusBadge(status: string): string {
  if (status === 'active') return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
  if (status === 'pending') return 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'
}

async function loadCards() {
  loading.value = true
  try {
    cards.value = await balanceCardsAPI.getMyBalanceCards()
  } catch (error: any) {
    appStore.showError(error?.message || t('balanceCards.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function togglePreference(card: UserBalanceCard, key: 'fallback_enabled' | 'auto_reset_enabled', event: Event) {
  const value = (event.target as HTMLInputElement).checked
  savingCardId.value = card.id
  try {
    const updated = await balanceCardsAPI.updateBalanceCardPreferences(card.id, { [key]: value })
    cards.value = cards.value.map(item => item.id === updated.id ? updated : item)
    appStore.showSuccess(t('common.saved'))
  } catch (error: any) {
    const input = event.target as HTMLInputElement
    input.checked = card[key]
    appStore.showError(error?.message || t('balanceCards.saveFailed'))
  } finally {
    savingCardId.value = null
  }
}

async function resetQuota(card: UserBalanceCard) {
  const resetWindow = balanceCardResetWindow(card)
  if (!resetWindow) return
  const confirmMessage = resetWindow === 'weekly'
    ? t('balanceCards.resetWeekConfirm', { days: balanceCardResetCostDays(card, resetWindow) })
    : t('balanceCards.resetConfirm')
  if (!window.confirm(confirmMessage)) return
  resettingCardId.value = card.id
  try {
    const updated = await balanceCardsAPI.resetBalanceCardDaily(card.id)
    cards.value = cards.value.map(item => item.id === updated.id ? updated : item)
    appStore.showSuccess(t(resetWindow === 'weekly' ? 'balanceCards.resetWeekSuccess' : 'balanceCards.resetSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || t('balanceCards.resetFailed'))
  } finally {
    resettingCardId.value = null
  }
}

async function openLedger(card: UserBalanceCard) {
  showLedger.value = true
  ledgerLoading.value = true
  try {
    ledger.value = (await balanceCardsAPI.getBalanceCardLedger(card.id)).items
  } catch (error: any) {
    ledger.value = []
    appStore.showError(error?.message || t('balanceCards.loadFailed'))
  } finally {
    ledgerLoading.value = false
  }
}

onMounted(loadCards)
</script>
