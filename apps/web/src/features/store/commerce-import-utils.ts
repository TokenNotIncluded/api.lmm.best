/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { marketQuota } from '@/features/tool-market/money'
import { displayAmountToQuota } from '@/lib/currency'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import type {
  CommerceImportListing,
  CommerceImportText,
} from './commerce-import-types'

export type CommerceImportPriceUnit = 'USD' | 'QUOTA'
const fixedUsdConfig = {
  ...DEFAULT_CURRENCY_CONFIG,
  currencyUnit: 'credit' as const,
  creditsPerUsd: 500000,
  creditsPerUsdExact: '500000',
}
export function commerceImportPriceQuota(
  input: string,
  unit: CommerceImportPriceUnit
): number | undefined {
  try {
    if (unit === 'QUOTA') {
      if (!/^\d+$/.test(input.trim())) return undefined
      return marketQuota(input, Number)
    }
    const text = input.trim()
    if (!/^\d+(\.\d{1,30})?$/.test(text) || text.length > 64) return undefined
    const [whole, fraction = ''] = text.split('.')
    const denominator = 10n ** BigInt(fraction.length)
    const scaled = BigInt(`${whole}${fraction}`) * 500000n
    // The existing converter floors ledger fractions; imports require exact units.
    if (scaled % denominator !== 0n) return undefined
    return marketQuota(text, (value) =>
      displayAmountToQuota(value, 'USD', fixedUsdConfig)
    )
  } catch {
    return undefined
  }
}
export function commerceImportText(
  value: CommerceImportText | undefined
): string {
  if (typeof value === 'string') return value
  if (!value || typeof value !== 'object') return ''
  // Match the server's deterministic mapping, so the preview is the saved text.
  const key =
    ['zh-CN', 'zh', 'en', 'en-US'].find(
      (locale) =>
        typeof value[locale] === 'string' && value[locale].trim() !== ''
    ) ||
    Object.keys(value)
      .sort()
      .find(
        (locale) =>
          typeof value[locale] === 'string' && value[locale].trim() !== ''
      )
  return key && typeof value[key] === 'string' ? value[key] : ''
}
export function commerceImportUnsupportedFields(
  listing: CommerceImportListing
): string[] {
  const fields: string[] = []
  const mapped = new Set([
    'name',
    'description',
    'public',
    'mode',
    'support_email',
  ])
  for (const key of Object.keys(listing.product)) {
    if (
      !mapped.has(key) &&
      listing.product[key] !== undefined &&
      listing.product[key] !== null
    ) {
      fields.push(`product.${key}`)
    }
  }
  if (typeof listing.product.name === 'object') {
    fields.push('product.name localization')
  }
  if (typeof listing.product.description === 'object') {
    fields.push('product.description localization')
  }
  const variantMapped = new Set([
    'id',
    'name',
    'description',
    'price',
    'currency',
    'enabled',
  ])
  for (const variant of listing.variants) {
    for (const key of Object.keys(variant)) {
      if (
        !variantMapped.has(key) &&
        variant[key] !== undefined &&
        variant[key] !== null
      ) {
        fields.push(`variants.${variant.id}.${key}`)
      }
    }
    if (typeof variant.name === 'object') {
      fields.push(`variants.${variant.id}.name localization`)
    }
    if (typeof variant.description === 'object') {
      fields.push(`variants.${variant.id}.description localization`)
    }
  }
  return fields
}
export function commerceImportCount(
  input: string,
  maximum: number
): number | undefined {
  if (!/^\d+$/.test(input)) return undefined
  const count = Number(input)
  return Number.isSafeInteger(count) && count >= 1 && count <= maximum
    ? count
    : undefined
}
export function commerceImportAuthorizationUrl(
  raw: string,
  issuer: string
): string | undefined {
  try {
    const url = new URL(raw)
    return url.protocol === 'https:' &&
      url.origin === issuer &&
      !url.username &&
      !url.password &&
      !url.hash
      ? url.href
      : undefined
  } catch {
    return undefined
  }
}
export function consumeCommerceImportCallback(): string | null {
  const url = new URL(window.location.href)
  if (!url.searchParams.has('commerce_import')) return null
  const values = url.searchParams.getAll('commerce_import')
  url.searchParams.delete('commerce_import')
  window.history.replaceState(
    window.history.state,
    '',
    `${url.pathname}${url.search}${url.hash}`
  )
  return values.length === 1 &&
    ['connected', 'denied', 'failed'].includes(values[0])
    ? values[0]
    : 'failed'
}
