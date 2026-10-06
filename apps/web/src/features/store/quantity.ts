/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StorePaymentMethod, StoreProduct } from './types'

export function storeQuantity(value: string): number | undefined {
  if (!/^[1-9]\d*$/.test(value)) return undefined
  const quantity = Number(value)
  return Number.isSafeInteger(quantity) ? quantity : undefined
}

export function storeCheckoutCapacity(
  product: Pick<StoreProduct, 'available_stock' | 'price_quota'>,
  method: StorePaymentMethod | ''
) {
  const { available_stock: stock, price_quota: price } = product
  if (
    !Number.isSafeInteger(stock) ||
    stock < 0 ||
    !Number.isSafeInteger(price) ||
    price < 1
  ) {
    return 0
  }
  let capacity = Math.min(
    stock,
    method === 'balance' ? 1000 : 100,
    Math.floor(Number.MAX_SAFE_INTEGER / price)
  )
  if (!Number.isSafeInteger(capacity * price)) capacity--
  return Math.max(0, capacity)
}
