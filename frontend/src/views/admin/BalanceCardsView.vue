<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="inline-flex rounded-xl bg-gray-100 p-1 dark:bg-dark-800">
          <button class="rounded-lg px-4 py-2 text-sm font-medium" :class="tab === 'cards' ? activeTabClass : inactiveTabClass" @click="tab = 'cards'">{{ t('balanceCards.admin.issuedCards') }}</button>
          <button class="rounded-lg px-4 py-2 text-sm font-medium" :class="tab === 'plans' ? activeTabClass : inactiveTabClass" @click="tab = 'plans'">{{ t('balanceCards.admin.plans') }}</button>
        </div>
        <div class="flex gap-2">
          <button class="btn btn-secondary" @click="refresh"><Icon name="refresh" size="md" /></button>
          <button v-if="tab === 'cards'" class="btn btn-primary" @click="openAssign"><Icon name="plus" size="md" class="mr-2" />{{ t('balanceCards.admin.assign') }}</button>
          <button v-else class="btn btn-primary" @click="openPlan()"><Icon name="plus" size="md" class="mr-2" />{{ t('balanceCards.admin.createPlan') }}</button>
        </div>
      </div>

      <section v-if="tab === 'cards'" class="space-y-4">
        <div class="card flex flex-wrap gap-3 p-4">
          <input v-model.number="filters.user_id" type="number" min="1" class="input w-full sm:w-44" :placeholder="t('balanceCards.admin.userId')" @keyup.enter="applyFilters" />
          <select v-model.number="filters.plan_id" class="input w-full sm:w-52" @change="applyFilters">
            <option :value="0">{{ t('balanceCards.admin.allPlans') }}</option>
            <option v-for="plan in plans" :key="plan.id" :value="plan.id">{{ plan.name }}</option>
          </select>
          <select v-model="filters.status" class="input w-full sm:w-44" @change="applyFilters">
            <option value="">{{ t('balanceCards.admin.allStatus') }}</option>
            <option v-for="status in cardStatuses" :key="status" :value="status">{{ t(`balanceCards.status.${status}`) }}</option>
          </select>
          <button class="btn btn-secondary" @click="applyFilters">{{ t('common.filter') }}</button>
        </div>

        <div class="card overflow-hidden">
          <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
              <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-800">
                <tr><th class="px-4 py-3">ID</th><th class="px-4 py-3">{{ t('balanceCards.admin.user') }}</th><th class="px-4 py-3">{{ t('balanceCards.admin.plan') }}</th><th class="px-4 py-3">{{ t('balanceCards.quotaUsage') }}</th><th class="px-4 py-3">{{ t('balanceCards.expiresAt') }}</th><th class="px-4 py-3">{{ t('common.status') }}</th><th class="px-4 py-3 text-right">{{ t('common.actions') }}</th></tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-if="loading"><td colspan="7" class="px-4 py-12 text-center text-gray-500">{{ t('common.loading') }}</td></tr>
                <tr v-for="card in cards" v-else :key="card.id" class="text-gray-700 dark:text-gray-300">
                  <td class="px-4 py-3">#{{ card.id }}</td>
                  <td class="px-4 py-3"><div class="font-medium">{{ card.user_email || `#${card.user_id}` }}</div><div v-if="card.user_email" class="text-xs text-gray-400">#{{ card.user_id }}</div></td>
                  <td class="px-4 py-3"><div class="font-medium">{{ card.plan_name }}</div><div class="text-xs text-gray-400">{{ t(`balanceCards.types.${card.card_type}`) }}</div></td>
	                  <td class="min-w-44 px-4 py-3"><div v-for="quota in cardQuotaRows(card)" :key="quota.key" class="mb-1.5 last:mb-0"><div class="flex justify-between gap-3 text-xs"><span>{{ quota.label }}</span><span v-if="quota.limit > 0">${{ quota.usage.toFixed(2) }} / ${{ quota.limit.toFixed(2) }}</span><span v-else class="text-emerald-600">{{ t('balanceCards.unlimited') }}</span></div><div v-if="quota.limit > 0" class="mt-1 h-1.5 overflow-hidden rounded bg-gray-200 dark:bg-dark-600"><div class="h-full bg-primary-500" :style="{ width: `${quotaPercent(quota.usage, quota.limit)}%` }" /></div></div></td>
                  <td class="whitespace-nowrap px-4 py-3">{{ formatDateTime(card.expires_at) }}</td>
                  <td class="px-4 py-3"><span class="rounded-full bg-gray-100 px-2 py-1 text-xs dark:bg-dark-700">{{ t(`balanceCards.status.${card.status}`) }}</span></td>
	                  <td class="px-4 py-3"><div class="flex justify-end gap-2"><button class="text-primary-600 hover:underline" @click="viewLedger(card)">{{ t('balanceCards.ledger') }}</button><button v-if="card.status === 'active' || card.status === 'pending'" class="text-primary-600 hover:underline" @click="extendCard(card)">{{ t('balanceCards.admin.extend') }}</button><button v-if="balanceCardResetWindow(card)" class="text-amber-600 hover:underline" @click="resetCard(card)">{{ balanceCardResetWindow(card) === 'weekly' ? t('balanceCards.resetWeek') : t('common.reset') }}</button><button v-if="card.status === 'active' || card.status === 'pending'" class="text-red-600 hover:underline" @click="revokeCard(card)">{{ t('balanceCards.admin.revoke') }}</button><button v-if="card.status === 'revoked'" class="text-red-600 hover:underline" @click="deleteCard(card)">{{ t('common.delete') }}</button></div></td>
                </tr>
                <tr v-if="!loading && cards.length === 0"><td colspan="7" class="px-4 py-12 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
              </tbody>
            </table>
          </div>
          <div class="flex items-center justify-between border-t border-gray-100 px-4 py-3 text-sm dark:border-dark-700">
            <span class="text-gray-500">{{ t('balanceCards.admin.total', { count: pagination.total }) }}</span>
            <div class="flex gap-2"><button class="btn btn-secondary px-3 py-1.5" :disabled="pagination.page <= 1" @click="changePage(pagination.page - 1)">{{ t('common.back') }}</button><span class="px-2 py-2">{{ pagination.page }} / {{ Math.max(1, pagination.pages) }}</span><button class="btn btn-secondary px-3 py-1.5" :disabled="pagination.page >= pagination.pages" @click="changePage(pagination.page + 1)">{{ t('common.next') }}</button></div>
          </div>
        </div>
      </section>

      <section v-else class="grid gap-5 md:grid-cols-2 xl:grid-cols-3">
        <div v-if="plansLoading" class="card col-span-full p-12 text-center text-gray-500">{{ t('common.loading') }}</div>
        <article v-for="plan in plans" v-else :key="plan.id" class="card p-5">
          <div class="flex items-start justify-between"><div><h3 class="font-semibold text-gray-900 dark:text-white">{{ plan.name }}</h3><p class="mt-1 text-xs text-gray-500">{{ t(`balanceCards.types.${plan.card_type}`) }}</p></div><span class="rounded-full px-2 py-1 text-xs" :class="plan.status === 'active' ? 'bg-emerald-100 text-emerald-700' : 'bg-gray-100 text-gray-500'">{{ t(`common.${plan.status}`) }}</span></div>
          <p class="mt-4 min-h-10 text-sm text-gray-500 dark:text-dark-400">{{ plan.description || '-' }}</p>
	          <dl class="mt-4 grid grid-cols-2 gap-3 text-sm"><div><dt class="text-xs text-gray-400">{{ t('balanceCards.admin.validity') }}</dt><dd class="mt-1 font-medium">{{ plan.validity_days }} {{ t('balanceCards.days') }}</dd></div><div><dt class="text-xs text-gray-400">{{ t('balanceCards.admin.dailyQuota') }}</dt><dd class="mt-1 font-medium">{{ quotaDisplay(plan.daily_quota_usd) }}</dd></div><div v-if="plan.card_type === 'week' || plan.card_type === 'month'"><dt class="text-xs text-gray-400">{{ plan.card_type === 'week' ? t('balanceCards.weekCardTotal') : t('balanceCards.weekQuota') }}</dt><dd class="mt-1 font-medium">{{ quotaDisplay(plan.weekly_quota_usd) }}</dd></div><div v-if="plan.card_type === 'month'"><dt class="text-xs text-gray-400">{{ t('balanceCards.monthCardTotal') }}</dt><dd class="mt-1 font-medium">{{ quotaDisplay(plan.monthly_quota_usd) }}</dd></div><div><dt class="text-xs text-gray-400">{{ t('balanceCards.fallback') }}</dt><dd class="mt-1">{{ plan.fallback_default ? t('common.enabled') : t('common.disabled') }}</dd></div><div><dt class="text-xs text-gray-400">{{ t('balanceCards.autoReset') }}</dt><dd class="mt-1">{{ plan.auto_reset_default ? t('common.enabled') : t('common.disabled') }}</dd></div></dl>
          <button class="btn btn-secondary mt-5 w-full" @click="openPlan(plan)">{{ t('common.edit') }}</button>
        </article>
      </section>
    </div>

    <BaseDialog :show="showPlanDialog" :title="editingPlanId ? t('balanceCards.admin.editPlan') : t('balanceCards.admin.createPlan')" width="wide" @close="showPlanDialog = false">
      <form class="grid gap-4 sm:grid-cols-2" @submit.prevent="savePlan">
        <label class="sm:col-span-2"><span class="input-label">{{ t('common.name') }}</span><input v-model.trim="planForm.name" class="input" required maxlength="100" /></label>
        <label class="sm:col-span-2"><span class="input-label">{{ t('balanceCards.admin.planDescription') }}</span><textarea v-model="planForm.description" class="input min-h-20" /></label>
        <label><span class="input-label">{{ t('balanceCards.admin.cardType') }}</span><select v-model="planForm.card_type" class="input"><option v-for="type in cardTypes" :key="type" :value="type">{{ t(`balanceCards.types.${type}`) }}</option></select></label>
        <label><span class="input-label">{{ t('balanceCards.admin.validity') }}</span><input v-model.number="planForm.validity_days" class="input" type="number" min="1" max="36500" required /></label>
	        <label><span class="input-label">{{ t('balanceCards.admin.dailyQuota') }}</span><input v-model.number="planForm.daily_quota_usd" class="input" type="number" min="0" step="0.01" :placeholder="t('balanceCards.admin.unlimitedQuotaHint')" /></label>
	        <label v-if="planForm.card_type === 'week' || planForm.card_type === 'month'"><span class="input-label">{{ planForm.card_type === 'week' ? t('balanceCards.weekCardTotal') : t('balanceCards.weekQuota') }}</span><input v-model.number="planForm.weekly_quota_usd" class="input" type="number" min="0" step="0.01" :placeholder="t('balanceCards.admin.unlimitedQuotaHint')" /></label>
	        <label v-if="planForm.card_type === 'month'"><span class="input-label">{{ t('balanceCards.monthCardTotal') }}</span><input v-model.number="planForm.monthly_quota_usd" class="input" type="number" min="0" step="0.01" :placeholder="t('balanceCards.admin.unlimitedQuotaHint')" /></label>
	        <p class="text-xs text-gray-500 sm:col-span-2">{{ t('balanceCards.admin.unlimitedQuotaHint') }}</p>
        <label><span class="input-label">{{ t('balanceCards.admin.maxResets') }}</span><input v-model.number="planForm.max_reset_count" class="input" type="number" min="0" max="1000" /></label>
        <label><span class="input-label">{{ t('common.status') }}</span><select v-model="planForm.status" class="input"><option value="active">{{ t('common.active') }}</option><option value="inactive">{{ t('common.inactive') }}</option></select></label>
        <label><span class="input-label">{{ t('balanceCards.admin.sortOrder') }}</span><input v-model.number="planForm.sort_order" class="input" type="number" /></label>
        <label class="flex items-center gap-3 rounded-xl bg-gray-50 p-3 dark:bg-dark-700"><input v-model="planForm.fallback_default" type="checkbox" class="h-5 w-5 rounded text-primary-600" /><span class="text-sm">{{ t('balanceCards.fallback') }}</span></label>
        <label class="flex items-center gap-3 rounded-xl bg-gray-50 p-3 dark:bg-dark-700"><input v-model="planForm.auto_reset_default" type="checkbox" class="h-5 w-5 rounded text-primary-600" /><span class="text-sm">{{ t('balanceCards.autoReset') }}</span></label>
        <div class="flex justify-end gap-3 sm:col-span-2"><button type="button" class="btn btn-secondary" @click="showPlanDialog = false">{{ t('common.cancel') }}</button><button type="submit" class="btn btn-primary" :disabled="submitting">{{ submitting ? t('common.submitting') : t('common.save') }}</button></div>
      </form>
    </BaseDialog>

    <BaseDialog :show="showAssignDialog" :title="t('balanceCards.admin.assign')" @close="showAssignDialog = false">
      <form class="space-y-4" @submit.prevent="assignCards">
	      <div>
	        <span class="input-label">{{ t('balanceCards.admin.userIds') }}</span>
	        <BalanceCardUserSelector v-model="assignForm.userIds" :disabled="submitting" />
	      </div>
        <label><span class="input-label">{{ t('balanceCards.admin.plan') }}</span><select v-model.number="assignForm.planId" class="input" required><option :value="0" disabled>{{ t('common.selectOption') }}</option><option v-for="plan in activePlans" :key="plan.id" :value="plan.id">{{ plan.name }} · {{ planQuotaSummary(plan) }}</option></select></label>
        <label><span class="input-label">{{ t('balanceCards.admin.notes') }}</span><textarea v-model="assignForm.notes" class="input min-h-20" /></label>
        <p class="text-xs text-gray-500">{{ t('balanceCards.admin.queueHint') }}</p>
        <div class="flex justify-end gap-3"><button type="button" class="btn btn-secondary" @click="showAssignDialog = false">{{ t('common.cancel') }}</button><button type="submit" class="btn btn-primary" :disabled="submitting">{{ submitting ? t('common.submitting') : t('balanceCards.admin.assign') }}</button></div>
      </form>
    </BaseDialog>

    <BaseDialog :show="showLedgerDialog" :title="t('balanceCards.ledger')" width="wide" @close="showLedgerDialog = false">
      <div class="overflow-x-auto"><table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700"><thead><tr class="text-left text-xs text-gray-500"><th class="px-3 py-2">{{ t('balanceCards.event') }}</th><th class="px-3 py-2">{{ t('balanceCards.amount') }}</th><th class="px-3 py-2">{{ t('balanceCards.usageAfter') }}</th><th class="px-3 py-2">{{ t('balanceCards.time') }}</th></tr></thead><tbody class="divide-y divide-gray-100 dark:divide-dark-700"><tr v-for="item in ledgerItems" :key="item.id"><td class="px-3 py-3">{{ t(`balanceCards.events.${item.event_type}`, item.event_type) }}</td><td class="px-3 py-3">${{ item.amount_usd.toFixed(4) }}</td><td class="px-3 py-3">${{ item.daily_usage_after.toFixed(4) }}</td><td class="whitespace-nowrap px-3 py-3">{{ formatDateTime(item.created_at) }}</td></tr><tr v-if="ledgerItems.length === 0"><td colspan="4" class="px-3 py-8 text-center text-gray-500">{{ t('common.noData') }}</td></tr></tbody></table></div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
	import BalanceCardUserSelector from '@/components/admin/balance-cards/BalanceCardUserSelector.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { balanceCardResetCostDays, balanceCardResetWindow } from '@/utils/balanceCardReset'
import type { BalanceCardLedger, BalanceCardPlan, BalanceCardPlanInput, BalanceCardType, UserBalanceCard } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const tab = ref<'cards' | 'plans'>('cards')
const activeTabClass = 'bg-white text-primary-600 shadow-sm dark:bg-dark-700 dark:text-primary-400'
const inactiveTabClass = 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-gray-200'
const cardTypes: BalanceCardType[] = ['day', 'week', 'month', 'custom']
const cardStatuses = ['pending', 'active', 'expired', 'suspended', 'revoked'] as const
const plans = ref<BalanceCardPlan[]>([])
const cards = ref<UserBalanceCard[]>([])
const loading = ref(false)
const plansLoading = ref(false)
const submitting = ref(false)
const pagination = reactive({ page: 1, page_size: 20, total: 0, pages: 0 })
const filters = reactive({ user_id: 0, plan_id: 0, status: '' })
const activePlans = computed(() => plans.value.filter(plan => plan.status === 'active'))

const showPlanDialog = ref(false)
const editingPlanId = ref<number | null>(null)
const planForm = reactive<BalanceCardPlanInput>(emptyPlan())
const showAssignDialog = ref(false)
	const assignForm = reactive({ userIds: [] as number[], planId: 0, notes: '' })
const showLedgerDialog = ref(false)
const ledgerItems = ref<BalanceCardLedger[]>([])

function emptyPlan(): BalanceCardPlanInput {
  return { name: '', description: '', card_type: 'month', validity_days: 30, daily_quota_usd: 10, weekly_quota_usd: 50, monthly_quota_usd: 200, fallback_default: true, auto_reset_default: false, max_reset_count: 20, status: 'active', sort_order: 0 }
}

function quotaPercent(usage: number, limit: number): number {
  return limit > 0 ? Math.min(100, Math.max(0, usage / limit * 100)) : 0
}

function cardQuotaRows(card: UserBalanceCard) {
  const rows = [{ key: 'daily', label: t('balanceCards.todayQuota'), usage: card.daily_usage_usd, limit: card.daily_quota_usd }]
	  if (card.card_type === 'week' || card.card_type === 'month') rows.push({ key: 'weekly', label: card.card_type === 'week' ? t('balanceCards.weekCardTotal') : t('balanceCards.weekQuota'), usage: card.weekly_usage_usd, limit: card.weekly_quota_usd })
	  if (card.card_type === 'month') rows.push({ key: 'monthly', label: t('balanceCards.monthCardTotal'), usage: card.monthly_usage_usd, limit: card.monthly_quota_usd })
  return rows
}

function quotaDisplay(limit: number): string {
  return limit > 0 ? `$${limit.toFixed(2)}` : t('balanceCards.unlimited')
}

function planQuotaSummary(plan: BalanceCardPlan): string {
	  const values = [`${t('balanceCards.todayQuota')} ${quotaDisplay(plan.daily_quota_usd)}`]
	  if (plan.card_type === 'week' || plan.card_type === 'month') values.push(`${plan.card_type === 'week' ? t('balanceCards.weekCardTotal') : t('balanceCards.weekQuota')} ${quotaDisplay(plan.weekly_quota_usd)}`)
	  if (plan.card_type === 'month') values.push(`${t('balanceCards.monthCardTotal')} ${quotaDisplay(plan.monthly_quota_usd)}`)
  return values.join(' / ')
}

function normalizedPlanInput(): BalanceCardPlanInput {
  const normalizeQuota = (value: unknown): number => {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : -1
  }
  return {
    ...planForm,
    daily_quota_usd: normalizeQuota(planForm.daily_quota_usd),
    weekly_quota_usd: normalizeQuota(planForm.weekly_quota_usd),
    monthly_quota_usd: normalizeQuota(planForm.monthly_quota_usd)
  }
}

async function loadPlans() {
  plansLoading.value = true
  try { plans.value = await adminAPI.balanceCards.listPlans() }
  catch (error: any) { appStore.showError(error?.message || t('balanceCards.loadFailed')) }
  finally { plansLoading.value = false }
}

async function loadCards() {
  loading.value = true
  try {
    const result = await adminAPI.balanceCards.listCards(pagination.page, pagination.page_size, {
      user_id: filters.user_id || undefined,
      plan_id: filters.plan_id || undefined,
      status: filters.status || undefined
    })
    cards.value = result.items
    Object.assign(pagination, { page: result.page, page_size: result.page_size, total: result.total, pages: result.pages })
  } catch (error: any) { appStore.showError(error?.message || t('balanceCards.loadFailed')) }
  finally { loading.value = false }
}

function refresh() { tab.value === 'cards' ? loadCards() : loadPlans() }
function applyFilters() { pagination.page = 1; loadCards() }
function changePage(page: number) { pagination.page = page; loadCards() }

function openPlan(plan?: BalanceCardPlan) {
  editingPlanId.value = plan?.id || null
  Object.assign(planForm, plan ? {
    name: plan.name, description: plan.description, card_type: plan.card_type,
    validity_days: plan.validity_days, daily_quota_usd: plan.daily_quota_usd,
    weekly_quota_usd: plan.weekly_quota_usd, monthly_quota_usd: plan.monthly_quota_usd,
    fallback_default: plan.fallback_default, auto_reset_default: plan.auto_reset_default,
    max_reset_count: plan.max_reset_count, status: plan.status, sort_order: plan.sort_order
  } : emptyPlan())
  showPlanDialog.value = true
}

async function savePlan() {
  submitting.value = true
  try {
	    const payload = normalizedPlanInput()
	    if (editingPlanId.value) await adminAPI.balanceCards.updatePlan(editingPlanId.value, payload)
	    else await adminAPI.balanceCards.createPlan(payload)
    showPlanDialog.value = false
    await loadPlans()
    appStore.showSuccess(t('common.saved'))
  } catch (error: any) { appStore.showError(error?.message || t('balanceCards.saveFailed')) }
  finally { submitting.value = false }
}

	function openAssign() { Object.assign(assignForm, { userIds: [], planId: activePlans.value[0]?.id || 0, notes: '' }); showAssignDialog.value = true }

async function assignCards() {
	  const userIds = [...new Set(assignForm.userIds.filter(id => Number.isInteger(id) && id > 0))]
  if (!userIds.length || !assignForm.planId) return appStore.showWarning(t('balanceCards.admin.invalidUsers'))
  submitting.value = true
  try {
    if (userIds.length === 1) await adminAPI.balanceCards.assign({ user_id: userIds[0], plan_id: assignForm.planId, notes: assignForm.notes })
    else await adminAPI.balanceCards.bulkAssign({ user_ids: userIds, plan_id: assignForm.planId, notes: assignForm.notes })
    showAssignDialog.value = false
    await loadCards()
    appStore.showSuccess(t('balanceCards.admin.assignSuccess', { count: userIds.length }))
  } catch (error: any) { appStore.showError(error?.message || t('balanceCards.admin.assignFailed')) }
  finally { submitting.value = false }
}

async function extendCard(card: UserBalanceCard) {
  const raw = window.prompt(t('balanceCards.admin.extendPrompt'), '1')
  if (raw === null) return
  const days = Number(raw)
  if (!Number.isInteger(days) || days === 0) return appStore.showWarning(t('balanceCards.admin.invalidDays'))
  try { await adminAPI.balanceCards.extend(card.id, days); await loadCards(); appStore.showSuccess(t('common.saved')) }
  catch (error: any) { appStore.showError(error?.message || t('balanceCards.saveFailed')) }
}

async function resetCard(card: UserBalanceCard) {
  const resetWindow = balanceCardResetWindow(card)
  if (!resetWindow) return
  const confirmMessage = resetWindow === 'weekly'
    ? t('balanceCards.resetWeekConfirm', { days: balanceCardResetCostDays(card, resetWindow) })
    : t('balanceCards.resetConfirm')
  if (!window.confirm(confirmMessage)) return
  try { await adminAPI.balanceCards.resetDaily(card.id); await loadCards(); appStore.showSuccess(t(resetWindow === 'weekly' ? 'balanceCards.resetWeekSuccess' : 'balanceCards.resetSuccess')) }
  catch (error: any) { appStore.showError(error?.message || t('balanceCards.resetFailed')) }
}

async function revokeCard(card: UserBalanceCard) {
  if (!window.confirm(t('balanceCards.admin.revokeConfirm'))) return
  try { await adminAPI.balanceCards.revoke(card.id); await loadCards(); appStore.showSuccess(t('common.saved')) }
  catch (error: any) { appStore.showError(error?.message || t('balanceCards.saveFailed')) }
}

async function deleteCard(card: UserBalanceCard) {
  if (!window.confirm(t('balanceCards.admin.deleteConfirm'))) return
  try {
    await adminAPI.balanceCards.delete(card.id)
    await loadCards()
    if (cards.value.length === 0 && pagination.page > 1) {
      pagination.page -= 1
      await loadCards()
    }
    appStore.showSuccess(t('balanceCards.admin.deleteSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || t('balanceCards.admin.deleteFailed'))
  }
}

async function viewLedger(card: UserBalanceCard) {
  showLedgerDialog.value = true
  ledgerItems.value = []
  try { ledgerItems.value = (await adminAPI.balanceCards.ledger(card.id)).items }
  catch (error: any) { appStore.showError(error?.message || t('balanceCards.loadFailed')) }
}

onMounted(async () => { await Promise.all([loadPlans(), loadCards()]) })
</script>
