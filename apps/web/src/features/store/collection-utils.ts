/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type {
  StoreCatalogueProduct,
  GuestStoreCartRef,
} from './catalogue-types'
import {
  storeVariantCapacity,
  storeVariantPrice,
  selectedStoreVariant,
} from './variant-utils'

export function storeCartCapacity(
  product: StoreCatalogueProduct,
  variantId: string
): number {
  const price = storeVariantPrice(product, variantId)
  if (!price || product.status !== 'published' || product.trading_paused) {
    return 0
  }
  let capacity = Math.min(
    storeVariantCapacity(product, variantId),
    Math.floor(Number.MAX_SAFE_INTEGER / price)
  )
  for (const limit of [
    product.unlimited_supply && product.sale_limit == null
      ? undefined
      : product.sale_available,
    product.max_quantity_per_order,
    product.buyer_purchase_remaining,
  ]) {
    if (limit !== null && limit !== undefined) {
      if (!Number.isSafeInteger(limit) || limit < 0) return 0
      capacity = Math.min(capacity, limit)
    }
  }
  if (
    product.max_quantity_per_buyer !== null &&
    product.max_quantity_per_buyer !== undefined
  ) {
    if (
      !Number.isSafeInteger(product.max_quantity_per_buyer) ||
      product.max_quantity_per_buyer < 1
    ) {
      return 0
    }
    capacity = Math.min(capacity, product.max_quantity_per_buyer)
  }
  return Math.max(0, capacity)
}

export function storeCartCheckoutUrl(ref: GuestStoreCartRef): string {
  const params = new URLSearchParams({
    variant_id: ref.variant_id,
    quantity: String(ref.quantity),
  })
  return `/store/products/${encodeURIComponent(ref.product_id)}?${params}`
}

export function storeCartVariantName(
  product: StoreCatalogueProduct,
  variantId: string
): string | undefined {
  return selectedStoreVariant(product, variantId)?.name
}
