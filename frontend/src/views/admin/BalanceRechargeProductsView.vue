<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div><h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('rechargeProducts.title') }}</h1><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('rechargeProducts.description') }}</p></div>
        <div class="flex flex-wrap gap-2">
          <RouterLink to="/purchase" class="btn btn-secondary">{{ t('rechargeProducts.preview') }}</RouterLink>
          <button class="btn btn-primary" :disabled="loading || loadFailed || saving || products.length >= 100" @click="openProduct()"><Icon name="plus" size="md" class="mr-2" />{{ t('rechargeProducts.create') }}</button>
        </div>
      </div>
      <div v-if="loading" class="card p-12 text-center text-gray-500" role="status">{{ t('common.loading') }}</div>
      <div v-else-if="loadFailed" class="card p-12 text-center">
        <p class="text-gray-500 dark:text-gray-400">{{ t('rechargeProducts.loadFailed') }}</p>
        <button class="btn btn-secondary mt-4" @click="loadCatalog">{{ t('common.tryAgain') }}</button>
      </div>
      <template v-else>
        <div class="card flex items-center justify-between gap-4 p-5">
          <div><label for="recharge-products-enabled" class="font-medium text-gray-900 dark:text-white">{{ t('rechargeProducts.enabled') }}</label><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('rechargeProducts.enabledHint') }}</p></div>
          <input id="recharge-products-enabled" type="checkbox" class="h-5 w-5 rounded text-primary-600" :checked="enabled" :disabled="saving" @change="toggleVisibility" />
        </div>
        <div v-if="products.length === 0" class="card p-12 text-center text-gray-500 dark:text-gray-400">{{ t('rechargeProducts.emptyAdmin') }}</div>
        <div v-else class="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3">
          <article v-for="product in products" :key="product.id" class="card flex flex-col p-5">
            <div class="flex items-start justify-between gap-3"><h2 class="break-words font-semibold text-gray-900 dark:text-white">{{ product.name }}</h2><span class="shrink-0 rounded-full px-2 py-1 text-xs" :class="product.for_sale ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700'">{{ t(product.for_sale ? 'rechargeProducts.onSale' : 'rechargeProducts.offSale') }}</span></div>
            <p class="mt-4 text-2xl font-bold text-primary-600 dark:text-primary-400">{{ formatPaymentAmount(product.price_cny, 'CNY') }}</p>
            <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ t('rechargeProducts.creditLabel') }}：{{ formatCredit(product.credit_usd) }}</p>
            <p class="mt-1 text-xs text-emerald-600 dark:text-emerald-400">{{ t('rechargeProducts.neverExpires') }}</p>
            <p v-if="product.description" class="mt-3 whitespace-pre-line break-words text-sm text-gray-500 dark:text-gray-400">{{ product.description }}</p>
            <p class="my-4 break-all text-xs text-gray-500 dark:text-gray-400">{{ product.purchase_url || t('rechargeProducts.contactAdmin') }}</p>
            <div class="mt-auto flex flex-wrap gap-2">
              <button class="btn btn-secondary" :disabled="saving" @click="openProduct(product)">{{ t('common.edit') }}</button>
              <button class="btn btn-secondary" :disabled="saving" @click="toggleProduct(product)">{{ t(product.for_sale ? 'rechargeProducts.unlist' : 'rechargeProducts.list') }}</button>
              <button class="btn btn-secondary text-red-600 dark:text-red-400" :disabled="saving" @click="deleteProduct(product)">{{ t('common.delete') }}</button>
            </div>
          </article>
        </div>
      </template>
    </div>

    <BaseDialog :show="showDialog" :title="t(editingId ? 'rechargeProducts.edit' : 'rechargeProducts.create')" @close="closeDialog">
      <form class="grid gap-4 sm:grid-cols-2" @submit.prevent="saveProduct">
        <label class="sm:col-span-2"><span class="input-label">{{ t('common.name') }}</span><input v-model.trim="form.name" class="input" required maxlength="100" /></label>
        <label class="sm:col-span-2"><span class="input-label">{{ t('rechargeProducts.productDescription') }}</span><textarea v-model.trim="form.description" class="input min-h-20" maxlength="2000" /></label>
        <label><span class="input-label">{{ t('rechargeProducts.priceLabel') }}</span><input v-model.number="form.price_cny" class="input" type="number" min="0.01" max="9999999999.99" step="0.01" required /></label>
        <label><span class="input-label">{{ t('rechargeProducts.creditLabel') }}</span><input v-model.number="form.credit_usd" class="input" type="number" min="0.00000001" max="9999999999.99" step="0.00000001" required /></label>
        <label class="sm:col-span-2"><span class="input-label">{{ t('rechargeProducts.purchaseURL') }}</span><input v-model.trim="form.purchase_url" class="input" type="url" maxlength="2048" pattern="https?://.+" placeholder="https://" /></label>
        <p class="text-xs leading-relaxed text-gray-500 dark:text-gray-400 sm:col-span-2">{{ t('rechargeProducts.linkHint') }}</p>
        <label><span class="input-label">{{ t('rechargeProducts.sortOrder') }}</span><input v-model.number="form.sort_order" class="input" type="number" min="0" max="100000" step="1" required /></label>
        <label class="flex items-center gap-3"><input v-model="form.for_sale" type="checkbox" class="h-5 w-5 rounded text-primary-600" /><span class="text-sm">{{ t('rechargeProducts.forSale') }}</span></label>
        <div class="flex justify-end gap-3 sm:col-span-2"><button type="button" class="btn btn-secondary" :disabled="saving" @click="closeDialog">{{ t('common.cancel') }}</button><button type="submit" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'common.submitting' : 'common.save') }}</button></div>
      </form>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatPaymentAmount } from '@/components/payment/currency'
import { getBalanceRechargeCatalog, updateBalanceRechargeCatalog } from '@/api/balanceRecharge'
import type { BalanceRechargeProduct } from '@/api/balanceRecharge'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()
const products = ref<BalanceRechargeProduct[]>([])
const enabled = ref(true)
const loading = ref(true)
const loadFailed = ref(false)
const saving = ref(false)
const showDialog = ref(false)
const editingId = ref('')

function emptyProduct(): BalanceRechargeProduct {
  return { id: '', name: '', description: '', price_cny: 1, credit_usd: 5, purchase_url: '', for_sale: false, sort_order: 0 }
}
const form = reactive(emptyProduct())

function formatCredit(value: number): string {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD', maximumFractionDigits: 8 }).format(value)
}

async function loadCatalog() {
  loading.value = true
  loadFailed.value = false
  try {
    const catalog = await getBalanceRechargeCatalog()
    products.value = catalog.products
    enabled.value = catalog.enabled
  } catch {
    loadFailed.value = true
  } finally {
    loading.value = false
  }
}

async function saveCatalog(nextProducts: BalanceRechargeProduct[], nextEnabled = enabled.value): Promise<boolean> {
  if (saving.value || loading.value || loadFailed.value) return false
  saving.value = true
  try {
    const catalog = await updateBalanceRechargeCatalog({ enabled: nextEnabled, products: nextProducts })
    products.value = catalog.products
    enabled.value = catalog.enabled
    appStore.showSuccess(t('common.saved'))
    return true
  } catch (error: unknown) {
    appStore.showError(error instanceof Error ? error.message : t('rechargeProducts.saveFailed'))
    return false
  } finally {
    saving.value = false
  }
}

function openProduct(product?: BalanceRechargeProduct) {
  editingId.value = product?.id || ''
  Object.assign(form, product || emptyProduct())
  showDialog.value = true
}

function toggleVisibility(event: Event) {
  const input = event.target as HTMLInputElement
  const nextEnabled = input.checked
  input.checked = enabled.value
  return saveCatalog(products.value, nextEnabled)
}

function closeDialog() {
  if (!saving.value) showDialog.value = false
}

async function saveProduct() {
  const product = { ...form, price_cny: Number(form.price_cny), credit_usd: Number(form.credit_usd), sort_order: Number(form.sort_order) }
  const next = editingId.value ? products.value.map(item => item.id === editingId.value ? product : item) : [...products.value, product]
  if (await saveCatalog(next)) showDialog.value = false
}

function toggleProduct(product: BalanceRechargeProduct) {
  return saveCatalog(products.value.map(item => item.id === product.id ? { ...item, for_sale: !item.for_sale } : item))
}

function deleteProduct(product: BalanceRechargeProduct) {
  if (window.confirm(t('rechargeProducts.deleteConfirm', { name: product.name }))) {
    return saveCatalog(products.value.filter(item => item.id !== product.id))
  }
}

onMounted(loadCatalog)
</script>
