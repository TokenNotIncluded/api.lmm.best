/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StorePaymentMethod, StoreProduct } from './types'

export function storeQuantity(value: string): number | undefined {
  if (!/^[1-9]\d*$/.test(value)) return undefined
  const quantity = Number(value)
  return Number.isSafeInteger(quantity) ? quantity : undefined
}

export function storeCheckoutCapacity(
  product: Pick<StoreProduct, 'available_stock' | 'price_quota'> &
    Partial<
      Pick<
        StoreProduct,
        | 'sale_available'
        | 'sale_limit'
        | 'unlimited_supply'
        | 'max_quantity_per_order'
        | 'max_quantity_per_buyer'
        | 'buyer_purchase_remaining'
      >
    >,
  method: StorePaymentMethod | ''
) {
  const { available_stock: stock, price_quota: price } = product
  const unlimited = product.unlimited_supply === true
  if (
    unlimited &&
    product.sale_limit != null &&
    product.sale_available == null
  ) {
    return 0
  }
  if (
    (!unlimited && (!Number.isSafeInteger(stock) || stock < 0)) ||
    !Number.isSafeInteger(price) ||
    price < 1
  ) {
    return 0
  }
  let capacity = Math.min(
    unlimited ? Infinity : stock,
    method === 'balance' ? 1000 : 100,
    Math.floor(Number.MAX_SAFE_INTEGER / price)
  )
  const orderLimit = product.max_quantity_per_order
  if (orderLimit !== undefined && orderLimit !== null) {
    if (!Number.isSafeInteger(orderLimit) || orderLimit < 1) return 0
    capacity = Math.min(capacity, orderLimit)
  }
  if (product.max_quantity_per_buyer != null) {
    if (
      !Number.isSafeInteger(product.max_quantity_per_buyer) ||
      product.max_quantity_per_buyer < 1 ||
      product.buyer_purchase_remaining == null
    ) {
      return 0
    }
    capacity = Math.min(capacity, product.max_quantity_per_buyer)
  }
  for (const available of [
    unlimited && product.sale_limit == null
      ? undefined
      : product.sale_available,
    product.buyer_purchase_remaining,
  ]) {
    if (available !== undefined && available !== null) {
      if (!Number.isSafeInteger(available) || available < 0) return 0
      capacity = Math.min(capacity, available)
    }
  }
  if (!Number.isSafeInteger(capacity * price)) capacity--
  return Math.max(0, capacity)
}

export function storeClampQuantity(value: string, capacity: number): string {
  return String(Math.min(storeQuantity(value) ?? 1, Math.max(1, capacity)))
}
