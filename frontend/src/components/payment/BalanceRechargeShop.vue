<template>
  <section v-if="loading || loadFailed || products.length > 0 || showEmpty" class="space-y-5" :aria-label="t('rechargeProducts.balanceRecharge')">
    <div v-if="loading" class="card p-12 text-center text-gray-500" role="status">{{ t('common.loading') }}</div>
    <div v-else-if="loadFailed" class="card p-12 text-center">
      <p class="text-gray-500 dark:text-gray-400">{{ t('rechargeProducts.loadFailed') }}</p>
      <button class="btn btn-secondary mt-4" @click="loadProducts">{{ t('common.tryAgain') }}</button>
    </div>
    <div v-else-if="products.length === 0 && showEmpty" class="card py-16 text-center">
      <p class="text-gray-500 dark:text-gray-400">{{ t('rechargeProducts.emptyShop') }}</p>
    </div>
    <div v-else class="grid grid-cols-1 gap-5 sm:grid-cols-2">
      <article v-for="product in products" :key="product.id" class="card flex flex-col overflow-hidden">
        <div class="h-1.5 bg-gradient-to-r from-primary-500 to-emerald-400"></div>
        <div class="flex flex-1 flex-col p-6 sm:p-7">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <span class="flex items-center gap-2 text-sm font-medium text-primary-600 dark:text-primary-400"><Icon name="creditCard" size="md" />{{ t('rechargeProducts.balanceRecharge') }}</span>
            <span class="rounded-full bg-emerald-50 px-3 py-1 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">{{ t('rechargeProducts.neverExpires') }}</span>
          </div>
          <h2 class="mt-5 break-words text-xl font-semibold text-gray-900 dark:text-white">{{ product.name }}</h2>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('rechargeProducts.rate', { rate: formatRate(product.price_cny / product.credit_usd), usd: formatRate(product.credit_usd / product.price_cny) }) }}</p>
          <div class="my-5 grid grid-cols-2 gap-4 rounded-xl bg-gray-50 p-5 dark:bg-dark-800">
            <div><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.paymentAmount') }}</p><p class="mt-2 break-words text-2xl font-bold text-gray-900 dark:text-white">{{ formatPaymentAmount(product.price_cny, 'CNY') }}</p></div>
            <div class="border-l border-gray-200 pl-4 dark:border-dark-600"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('payment.creditedBalance') }}</p><p class="mt-2 break-words text-2xl font-bold text-primary-600 dark:text-primary-400">{{ formatCredit(product.credit_usd) }}</p></div>
          </div>
          <p v-if="product.description" class="mb-5 whitespace-pre-line break-words text-sm leading-relaxed text-gray-500 dark:text-gray-400">{{ product.description }}</p>
          <a v-if="safePurchaseURL(product.purchase_url)" :href="safePurchaseURL(product.purchase_url)" target="_blank" rel="noopener noreferrer" class="btn btn-primary mt-auto w-full justify-center gap-2">
            {{ t('rechargeProducts.buy') }}<Icon name="externalLink" size="sm" />
          </a>
          <p v-else class="mt-auto rounded-xl bg-primary-50 px-4 py-3 text-center text-sm font-semibold text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{{ t('rechargeProducts.contactAdmin') }}</p>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getBalanceRechargeProducts } from '@/api/balanceRecharge'
import type { BalanceRechargeProduct } from '@/api/balanceRecharge'
import Icon from '@/components/icons/Icon.vue'
import { formatPaymentAmount } from './currency'
import { safePurchaseURL } from './purchaseLink'

defineProps<{ showEmpty: boolean }>()
const { t } = useI18n()
const products = ref<BalanceRechargeProduct[]>([])
const loading = ref(true)
const loadFailed = ref(false)

function formatRate(value: number): string {
  return new Intl.NumberFormat(undefined, { maximumSignificantDigits: 6, useGrouping: false }).format(value)
}

function formatCredit(value: number): string {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD', maximumFractionDigits: 8 }).format(value)
}

async function loadProducts() {
  loading.value = true
  loadFailed.value = false
  try {
    products.value = await getBalanceRechargeProducts()
  } catch {
    loadFailed.value = true
  } finally {
    loading.value = false
  }
}

onMounted(loadProducts)
</script>
