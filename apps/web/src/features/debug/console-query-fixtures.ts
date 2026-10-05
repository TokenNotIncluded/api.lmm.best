/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { AxiosAdapter } from 'axios'

import {
  DEBUG_CURRENCY_STATUS,
  DEBUG_DISCOUNT_CODE,
  debugCreditGrant,
  debugSettlementQuote,
  DEBUG_WALLET_TOPUP_INFO,
} from './wallet-review-fixtures'

const creditQuoteRoutes: Record<
  string,
  {
    currency: 'CNY' | 'USD'
    paymentMethod: string
    minimum: number
    maximum: number | null
  }
> = {
  '/api/user/topup/currency/v2/amount': {
    currency: 'CNY',
    paymentMethod: 'alipay',
    minimum: 3500000,
    maximum: 350000000,
  },
  '/api/user/topup/currency/v2/stripe/amount': {
    currency: 'USD',
    paymentMethod: 'stripe',
    minimum: DEBUG_WALLET_TOPUP_INFO.stripe_credit_min_topup,
    maximum: DEBUG_WALLET_TOPUP_INFO.stripe_credit_max_topup,
  },
  '/api/user/topup/currency/v2/waffo/amount': {
    currency: 'USD',
    paymentMethod: 'waffo',
    minimum: DEBUG_WALLET_TOPUP_INFO.waffo_credit_min_topup,
    maximum: DEBUG_WALLET_TOPUP_INFO.waffo_credit_max_topup,
  },
  '/api/user/topup/currency/v2/waffo-pancake/amount': {
    currency: 'USD',
    paymentMethod: 'waffo_pancake',
    minimum: DEBUG_WALLET_TOPUP_INFO.pancake_credit_min_topup,
    maximum: DEBUG_WALLET_TOPUP_INFO.pancake_credit_max_topup,
  },
}

// Some read-only queries use POST for model lists or a local payment quote. Keep
// these exact paths separate from the general GET fixtures; no purchase,
// payment, reset, refund, or administrative mutation is permitted.
export function withConsoleQueryFixtures(fallback: AxiosAdapter): AxiosAdapter {
  return async (config) => {
    const url = new URL(config.url ?? '', window.location.origin)
    const method = (config.method ?? 'get').toUpperCase()
    if (url.origin !== window.location.origin || url.username || url.password) {
      return fallback(config)
    }

    let data: unknown
    if (method === 'POST' && url.pathname === '/api/pricing/runtime') {
      data = { success: true, data: {} }
    } else if (
      method === 'POST' &&
      Object.hasOwn(creditQuoteRoutes, url.pathname)
    ) {
      const route = creditQuoteRoutes[url.pathname]
      let body: unknown = config.data
      if (typeof body === 'string') {
        try {
          body = JSON.parse(body)
        } catch {
          return fallback(config)
        }
      }
      if (!body || typeof body !== 'object' || Array.isArray(body)) {
        return fallback(config)
      }
      const request = body as Record<string, unknown>
      const amount = request.amount
      if (
        typeof amount !== 'number' ||
        !Number.isSafeInteger(amount) ||
        amount <= 0 ||
        amount < route.minimum ||
        (route.maximum !== null && amount > route.maximum) ||
        request.amount_unit !== 'LEDGER_QUOTA' ||
        request.credit_metadata_version !== 2 ||
        (request.payment_method !== undefined &&
          request.payment_method !== route.paymentMethod) ||
        (request.discount_code !== undefined &&
          request.discount_code !== '' &&
          request.discount_code !== DEBUG_DISCOUNT_CODE) ||
        Object.keys(request).some(
          (key) =>
            ![
              'amount',
              'amount_unit',
              'credit_metadata_version',
              'payment_method',
              'discount_code',
            ].includes(key)
        )
      ) {
        return fallback(config)
      }
      // Exactly four synthetic amount queries. Checkout and payment writes
      // still reach the blocking adapter; no URL, key or provider is involved.
      data = {
        success: true,
        ...debugCreditGrant(amount),
        ...debugSettlementQuote(
          amount,
          route.currency,
          String(request.discount_code ?? '')
        ),
        settlement_currency: route.currency,
        credited_quota: amount,
        credit_amount: amount,
        amount_unit: 'LEDGER_QUOTA',
        legacy_batch_units: String(
          amount / DEBUG_CURRENCY_STATUS.quota_per_unit
        ),
      }
    } else if (
      method === 'POST' &&
      url.pathname === '/api/user/topup/currency/v2/discount-code/validate'
    ) {
      let request: unknown = config.data
      if (typeof request === 'string') {
        try {
          request = JSON.parse(request)
        } catch {
          return fallback(config)
        }
      }
      if (!request || typeof request !== 'object' || Array.isArray(request)) {
        return fallback(config)
      }
      const row = request as Record<string, unknown>
      if (
        row.amount_unit !== 'LEDGER_QUOTA' ||
        row.credit_metadata_version !== 2 ||
        typeof row.amount !== 'number' ||
        !Number.isSafeInteger(row.amount) ||
        row.amount < 3500000 ||
        row.amount > 350000000 ||
        Object.keys(row).some(
          (key) =>
            ![
              'amount',
              'amount_unit',
              'credit_metadata_version',
              'code',
            ].includes(key)
        )
      ) {
        return fallback(config)
      }
      data =
        row.code === DEBUG_DISCOUNT_CODE
          ? {
              success: true,
              data: {
                code: DEBUG_DISCOUNT_CODE,
                discount_percent: 20,
                min_amount: 0,
              },
            }
          : { success: false, message: 'Invalid code' }
    } else if (
      method === 'GET' &&
      url.pathname === '/api/subscription/root/reset-targets'
    ) {
      data = {
        success: true,
        data: { items: [], total: 0, page: 1, page_size: 20 },
      }
    } else {
      return fallback(config)
    }
    return {
      data,
      status: 200,
      statusText: 'Local read-only query fixture',
      headers: {},
      config,
    }
  }
}
