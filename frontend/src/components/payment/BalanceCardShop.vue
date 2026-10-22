<template>
  <section class="space-y-5" aria-labelledby="balance-card-shop-title">
    <div>
      <h2 id="balance-card-shop-title" class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('balanceCards.shop.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('balanceCards.shop.description') }}</p>
    </div>
    <div v-if="loading" class="card p-12 text-center text-gray-500" role="status">{{ t('common.loading') }}</div>
    <div v-else-if="loadFailed" class="card p-12 text-center">
      <p class="text-gray-500 dark:text-gray-400">{{ t('balanceCards.shop.loadFailed') }}</p>
      <button class="btn btn-secondary mt-4" @click="loadProducts">{{ t('common.tryAgain') }}</button>
    </div>
    <div v-else-if="products.length === 0" class="card p-12 text-center">
      <Icon name="creditCard" size="xl" class="mx-auto mb-3 text-gray-300 dark:text-dark-600" />
      <p class="font-medium text-gray-700 dark:text-gray-200">{{ t('balanceCards.shop.empty') }}</p>
      <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('balanceCards.shop.contactAdmin') }}</p>
    </div>
    <div v-else class="grid grid-cols-1 gap-5 sm:grid-cols-2">
      <article v-for="product in products" :key="product.id" class="card flex flex-col overflow-hidden">
        <div class="h-1.5 bg-gradient-to-r from-primary-500 to-emerald-400"></div>
        <div class="flex flex-1 flex-col p-6">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="rounded-full bg-primary-50 px-3 py-1 text-xs font-medium text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{{ t(`balanceCards.types.${product.card_type}`) }}</span>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ product.validity_days }} {{ t('balanceCards.days') }}</span>
          </div>
          <h3 class="mt-4 break-words text-xl font-semibold text-gray-900 dark:text-white">{{ product.name }}</h3>
          <p v-if="product.description" class="mt-2 whitespace-pre-line break-words text-sm leading-relaxed text-gray-500 dark:text-gray-400">{{ product.description }}</p>
          <p class="mt-5 text-3xl font-bold text-primary-600 dark:text-primary-400">{{ formatPaymentAmount(product.price_cny, 'CNY') }}</p>
          <dl class="my-5 space-y-3 rounded-xl bg-gray-50 p-4 text-sm dark:bg-dark-800">
            <div class="flex justify-between gap-3"><dt class="text-gray-500 dark:text-gray-400">{{ t('balanceCards.admin.dailyQuota') }}</dt><dd class="font-medium text-gray-900 dark:text-white">{{ quotaDisplay(product.daily_quota_usd) }}</dd></div>
            <div v-if="product.card_type === 'week' || product.card_type === 'month'" class="flex justify-between gap-3"><dt class="text-gray-500 dark:text-gray-400">{{ product.card_type === 'week' ? t('balanceCards.weekCardTotal') : t('balanceCards.weekQuota') }}</dt><dd class="font-medium text-gray-900 dark:text-white">{{ quotaDisplay(product.weekly_quota_usd) }}</dd></div>
            <div v-if="product.card_type === 'month'" class="flex justify-between gap-3"><dt class="text-gray-500 dark:text-gray-400">{{ t('balanceCards.monthCardTotal') }}</dt><dd class="font-medium text-gray-900 dark:text-white">{{ quotaDisplay(product.monthly_quota_usd) }}</dd></div>
            <div class="flex justify-between gap-3"><dt class="text-gray-500 dark:text-gray-400">{{ t('balanceCards.admin.maxResets') }}</dt><dd class="font-medium text-gray-900 dark:text-white">{{ product.max_reset_count }}</dd></div>
          </dl>
          <a v-if="safePurchaseURL(product.purchase_url)" :href="safePurchaseURL(product.purchase_url)" target="_blank" rel="noopener noreferrer" class="btn btn-primary mt-auto w-full justify-center gap-2">
            {{ t('balanceCards.shop.buy') }}<Icon name="externalLink" size="sm" />
          </a>
          <p v-else class="mt-auto rounded-xl bg-primary-50 px-4 py-3 text-center text-sm font-semibold text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{{ t('balanceCards.shop.contactAdmin') }}</p>
        </div>
      </article>
    </div>
    <div class="card p-5 text-sm text-gray-500 dark:text-gray-400">
      <p>{{ t('balanceCards.shop.redeemHint') }} <RouterLink to="/redeem" class="font-medium text-primary-600 hover:underline dark:text-primary-400">{{ t('balanceCards.shop.redeem') }}</RouterLink></p>
      <p class="mt-2">{{ t('balanceCards.admin.queueHint') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getBalanceCardProducts } from '@/api/balanceCards'
import type { BalanceCardProduct } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import { formatPaymentAmount } from './currency'
import { safePurchaseURL } from './purchaseLink'

const { t } = useI18n()
const products = ref<BalanceCardProduct[]>([])
const loading = ref(true)
const loadFailed = ref(false)

function quotaDisplay(value: number): string {
  return value > 0 ? formatPaymentAmount(value, 'USD') : t('balanceCards.unlimited')
}

async function loadProducts() {
  loading.value = true
  loadFailed.value = false
  try {
    products.value = await getBalanceCardProducts()
  } catch {
    loadFailed.value = true
  } finally {
    loading.value = false
  }
}

onMounted(loadProducts)
</script>
