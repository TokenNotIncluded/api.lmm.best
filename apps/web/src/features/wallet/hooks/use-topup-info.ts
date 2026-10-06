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
import { useState, useEffect, useCallback } from 'react'

import { getTopupInfo } from '../api'
import {
  generatePresetAmounts,
  mergePresetAmounts,
  getMinTopupAmount,
  getDedicatedPaymentLimits,
} from '../lib'
import {
  creditProjection,
  hasCompletePublicCreditCatalog,
} from '../lib/topup-credit-metadata'
import type {
  TopupInfo,
  PresetAmount,
  CreemProduct,
  PaymentMethod,
  WaffoPayMethod,
} from '../types'

// ============================================================================
// Topup Info Hook
// ============================================================================

function parseJsonArray(data: unknown): unknown[] {
  if (Array.isArray(data)) {
    return data
  }

  if (typeof data === 'string') {
    try {
      const parsed = JSON.parse(data)
      return Array.isArray(parsed) ? parsed : []
    } catch {
      return []
    }
  }

  return []
}

function parseStringOrNumber(value: unknown): string | number | undefined {
  return typeof value === 'string' || typeof value === 'number'
    ? value
    : undefined
}

function parsePaymentMethods(
  data: unknown,
  stripeMinTopup: number
): PaymentMethod[] {
  return parseJsonArray(data)
    .filter(
      (item): item is Record<string, unknown> =>
        !!item && typeof item === 'object'
    )
    .map((item): PaymentMethod => {
      const rawMinTopup = Number(item.min_topup)
      const normalizedMinTopup = Number.isFinite(rawMinTopup) ? rawMinTopup : 0
      const type = typeof item.type === 'string' ? item.type : ''

      return {
        name: typeof item.name === 'string' ? item.name : '',
        type,
        color: typeof item.color === 'string' ? item.color : undefined,
        description:
          typeof item.description === 'string' ? item.description : undefined,
        icon: typeof item.icon === 'string' ? item.icon : undefined,
        settlement_currency:
          typeof item.settlement_currency === 'string'
            ? item.settlement_currency
            : undefined,
        platform_units_per_usd: parseStringOrNumber(
          item.platform_units_per_usd
        ),
        settlement_units_per_usd: parseStringOrNumber(
          item.settlement_units_per_usd
        ),
        settlement_units_per_platform_unit: parseStringOrNumber(
          item.settlement_units_per_platform_unit
        ),
        settlement_unit:
          typeof item.settlement_unit === 'string'
            ? item.settlement_unit
            : undefined,
        unit_price: parseStringOrNumber(item.unit_price),
        topup_ratio: parseStringOrNumber(item.topup_ratio),
        max_topup: parseStringOrNumber(item.max_topup),
        max_topup_amount: parseStringOrNumber(item.max_topup_amount),
        min_topup_credit: parseStringOrNumber(item.min_topup_credit),
        max_topup_credit: parseStringOrNumber(item.max_topup_credit),
        min_topup_ledger_quota:
          typeof item.min_topup_ledger_quota === 'string'
            ? item.min_topup_ledger_quota
            : undefined,
        max_topup_ledger_quota:
          typeof item.max_topup_ledger_quota === 'string'
            ? item.max_topup_ledger_quota
            : undefined,
        min_topup_public_credit:
          typeof item.min_topup_public_credit === 'string'
            ? item.min_topup_public_credit
            : undefined,
        max_topup_public_credit:
          typeof item.max_topup_public_credit === 'string'
            ? item.max_topup_public_credit
            : undefined,
        credit_amount_unit:
          item.credit_amount_unit === 'LEDGER_QUOTA'
            ? 'LEDGER_QUOTA'
            : undefined,
        legacy_min_topup: parseStringOrNumber(item.legacy_min_topup),
        legacy_max_topup_amount: parseStringOrNumber(
          item.legacy_max_topup_amount
        ),
        min_topup_unit:
          item.min_topup_unit === 'USD'
            ? 'USD'
            : item.min_topup_unit === 'LEGACY'
              ? 'LEGACY'
              : undefined,
        max_topup_amount_unit:
          item.max_topup_amount_unit === 'CREDIT'
            ? 'CREDIT'
            : item.max_topup_amount_unit === 'LEGACY'
              ? 'LEGACY'
              : undefined,
        min_topup:
          type === 'stripe' && normalizedMinTopup <= 0
            ? stripeMinTopup
            : normalizedMinTopup,
      }
    })
    .filter((item) => item.name && item.type && item.type !== 'waffo')
}

function parseWaffoPayMethods(data: unknown): WaffoPayMethod[] {
  return parseJsonArray(data)
    .filter(
      (item): item is Record<string, unknown> =>
        !!item && typeof item === 'object'
    )
    .map((item) => ({
      name: typeof item.name === 'string' ? item.name : '',
      icon: typeof item.icon === 'string' ? item.icon : undefined,
      payMethodType:
        typeof item.payMethodType === 'string' ? item.payMethodType : undefined,
      payMethodName:
        typeof item.payMethodName === 'string' ? item.payMethodName : undefined,
    }))
    .filter((item) => item.name)
}

function parseCreemProducts(data: unknown): CreemProduct[] {
  return parseJsonArray(data)
    .filter(
      (item): item is Record<string, unknown> =>
        !!item && typeof item === 'object'
    )
    .map((item) => {
      const currency: CreemProduct['currency'] =
        item.currency === 'EUR' ? 'EUR' : 'USD'

      return {
        name: typeof item.name === 'string' ? item.name : '',
        productId: typeof item.productId === 'string' ? item.productId : '',
        price: Number(item.price) || 0,
        quota: Number(item.quota) || 0,
        currency,
      }
    })
    .filter((item) => item.name && item.productId)
}

function parseDiscountMap(data: unknown): Record<number, number> {
  if (!data) {
    return {}
  }

  let parsedData = data

  if (typeof data === 'string') {
    try {
      parsedData = JSON.parse(data)
    } catch {
      return {}
    }
  }

  if (
    !parsedData ||
    typeof parsedData !== 'object' ||
    Array.isArray(parsedData)
  ) {
    return {}
  }

  return Object.entries(parsedData).reduce<Record<number, number>>(
    (result, [key, value]) => {
      const numericKey = Number(key)
      const numericValue = Number(value)

      if (Number.isFinite(numericKey) && Number.isFinite(numericValue)) {
        result[numericKey] = numericValue
      }

      return result
    },
    {}
  )
}

export function useTopupInfo() {
  const [topupInfo, setTopupInfo] = useState<TopupInfo | null>(null)
  const [presetAmounts, setPresetAmounts] = useState<PresetAmount[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)

  const fetchTopupInfo = useCallback(async () => {
    try {
      setLoading(true)
      setError(null)

      const response = await getTopupInfo()

      if (!response.success || !response.data) {
        const nextError = new Error(
          response.message || 'Failed to fetch topup info'
        )
        setTopupInfo(null)
        setPresetAmounts([])
        setError(nextError)
        // eslint-disable-next-line no-console
        console.error('Failed to fetch topup info:', response.message)
        return
      }

      // Public projection strings are display-only; grants and discounts keep
      // their paired legacy ledger integers. Partial/v1 metadata cannot enable v2 money.
      const raw = response.data
      const isQuota = (value: unknown): value is number =>
        typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
      const creditMetadataReady = hasCompletePublicCreditCatalog(raw)
      const project = creditMetadataReady ? creditProjection(raw) : null
      const amountOptions =
        creditMetadataReady && Array.isArray(raw.ledger_quota_amount_options)
          ? raw.ledger_quota_amount_options
          : []
      const discountCatalog = creditMetadataReady
        ? Object.fromEntries(
            Object.entries(parseDiscountMap(raw.ledger_quota_discount)).filter(
              ([key]) => Number.isSafeInteger(Number(key)) && Number(key) > 0
            )
          )
        : {}
      const minimum = (value: unknown) =>
        creditMetadataReady && isQuota(value) ? value : 0
      const processedData: TopupInfo = {
        ...response.data,
        topup_group_ratio: (() => {
          const ratio = Number(response.data.topup_group_ratio)
          return Number.isFinite(ratio) && ratio > 0 ? ratio : 1
        })(),
        enable_online_topup: creditMetadataReady && raw.enable_online_topup,
        enable_stripe_topup:
          creditMetadataReady &&
          raw.enable_stripe_topup &&
          getDedicatedPaymentLimits(raw, 'stripe') !== null,
        enable_waffo_topup:
          creditMetadataReady &&
          raw.enable_waffo_topup &&
          getDedicatedPaymentLimits(raw, 'waffo') !== null,
        enable_waffo_pancake_topup:
          creditMetadataReady &&
          raw.enable_waffo_pancake_topup &&
          getDedicatedPaymentLimits(raw, 'waffo_pancake') !== null,
        pay_methods: creditMetadataReady
          ? parsePaymentMethods(raw.pay_methods, raw.stripe_min_topup).map(
              (method) => {
                const limits = getDedicatedPaymentLimits(raw, method.type)
                return limits
                  ? {
                      ...method,
                      min_topup_credit: limits.minimum,
                      max_topup_credit: limits.maximum ?? undefined,
                      min_topup_ledger_quota: String(limits.minimum),
                      max_topup_ledger_quota:
                        limits.maximum === null
                          ? undefined
                          : String(limits.maximum),
                      min_topup_public_credit: project?.(limits.minimum),
                      max_topup_public_credit:
                        limits.maximum === null
                          ? undefined
                          : project?.(limits.maximum),
                    }
                  : method
              }
            )
          : [],
        amount_unit: 'CREDIT',
        min_topup: minimum(raw.ledger_quota_min_topup),
        stripe_min_topup: minimum(raw.stripe_ledger_quota_min_topup),
        waffo_min_topup: minimum(raw.waffo_ledger_quota_min_topup),
        waffo_pancake_min_topup: minimum(raw.pancake_ledger_quota_min_topup),
        amount_options: amountOptions.filter(
          (quota) => Number.isSafeInteger(quota) && quota > 0
        ),
        discount: discountCatalog,
        creem_products: parseCreemProducts(response.data.creem_products),
        waffo_pay_methods: parseWaffoPayMethods(
          response.data.waffo_pay_methods
        ),
      }

      setTopupInfo(processedData)

      if (!creditMetadataReady) {
        setPresetAmounts([])
      } else if (processedData.amount_options.length > 0) {
        const customPresets = mergePresetAmounts(
          processedData.amount_options,
          processedData.discount || {}
        )
        setPresetAmounts(customPresets)
      } else {
        const minTopup = getMinTopupAmount(processedData)
        const defaultPresets = generatePresetAmounts(minTopup)
        setPresetAmounts(defaultPresets)
      }
    } catch (err) {
      setTopupInfo(null)
      setPresetAmounts([])
      setError(
        err instanceof Error ? err : new Error('Failed to fetch topup info')
      )
      // eslint-disable-next-line no-console
      console.error('Failed to fetch topup info:', err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    let cancelled = false

    queueMicrotask(() => {
      if (!cancelled) void fetchTopupInfo()
    })

    return () => {
      cancelled = true
    }
  }, [fetchTopupInfo])

  return {
    topupInfo,
    presetAmounts,
    loading,
    error,
    refetch: fetchTopupInfo,
  }
}
