/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StorePrice } from './types'

export const STORE_NATIVE_COPY = {
  unknown: '商店原生投影：金额未知',
  time: '商店原生投影：时间未知',
  readonly: '商店原生投影：只读说明',
  minor: '商店原生投影：原始货币最小单位',
} as const

export function nativePriceText(price: StorePrice | null | undefined) {
  if (!price) return undefined
  if (price.unit === 'credits') {
    if (price.currency != null || price.amount_minor != null) return undefined
    return typeof price.amount_credits === 'number' &&
      Number.isSafeInteger(price.amount_credits) &&
      price.amount_credits >= 0
      ? `${price.amount_credits} credits`
      : undefined
  }
  if (
    (price.unit != null && price.unit !== 'currency') ||
    price.amount_credits != null
  ) {
    return undefined
  }
  return typeof price.amount_minor === 'number' &&
    Number.isSafeInteger(price.amount_minor) &&
    price.amount_minor >= 0 &&
    typeof price.currency === 'string' &&
    /^[A-Z]{3}$/.test(price.currency)
    ? `${price.amount_minor} ${price.currency}`
    : undefined
}
