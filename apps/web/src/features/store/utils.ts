/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { toIntlLocale } from '@/i18n/languages'

import type { StorePaymentMethod } from './types'

export const CREDITS_PER_USD = 500000
export const EXTERNAL_MINIMUM_QUOTA = 10 * CREDITS_PER_USD
export const MAX_IMPORT_BYTES = 2 * 1024 * 1024
export const MAX_IMPORT_ITEMS = 10000

export function isStoreEmail(value: string) {
  return value.length <= 254 && /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)
}

export function safeStoreUrl(value: string): string | undefined {
  try {
    const url = new URL(value)
    return ['https:', 'http:'].includes(url.protocol) &&
      !url.username &&
      !url.password
      ? url.href
      : undefined
  } catch {
    return undefined
  }
}
export function parseInventoryText(text: string): string[] {
  if (new TextEncoder().encode(text).length > MAX_IMPORT_BYTES) {
    throw new Error('Inventory import is too large')
  }
  const items = text
    .replace(/^\uFEFF/, '')
    .split(/\r?\n/)
    .filter((line) => line.trim().length > 0)
  if (
    items.length > MAX_IMPORT_ITEMS ||
    items.some((line) => new TextEncoder().encode(line).length > 32768)
  ) {
    throw new Error('Inventory import is too large')
  }
  return items
}
export function storeTotal(unit: number, quantity: number) {
  if (
    !Number.isSafeInteger(unit) ||
    unit < 1 ||
    !Number.isSafeInteger(quantity) ||
    quantity < 1 ||
    !Number.isSafeInteger(unit * quantity)
  ) {
    throw new Error('Invalid amount')
  }
  return unit * quantity
}
export function storeDate(timestamp: number, locale: string) {
  return timestamp > 0
    ? new Date(timestamp * 1000).toLocaleString(toIntlLocale(locale))
    : '—'
}
export function storeRequestKey() {
  return crypto.randomUUID()
}
export function continueStorePayment(
  session?: import('./types').StorePaymentSession,
  legacyUrl?: string
) {
  const url = safeStoreUrl(session?.payment_url || legacyUrl || '')
  if (!url || !url.startsWith('https:')) {
    throw new Error('Payment link is unavailable')
  }
  if (session?.method === 'POST') {
    const form = document.createElement('form')
    form.method = 'POST'
    form.action = url
    for (const [name, value] of Object.entries(session.parameters || {})) {
      const input = document.createElement('input')
      input.type = 'hidden'
      input.name = name
      input.value = String(value)
      form.append(input)
    }
    document.body.append(form)
    form.submit()
    form.remove()
  } else {
    window.location.assign(url)
  }
}

export function paymentLabel(method: StorePaymentMethod | 'free') {
  if (method === 'free') return 'Free claim'
  if (method === 'balance') return 'Balance payment'
  if (method.endsWith('waffo_pancake')) return 'Waffo Pancake'
  if (method === 'platform:linuxdo') return 'Linux DO payment'
  return 'External payment'
}
