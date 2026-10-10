/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StoreProduct, StoreVariant } from './types'

export function legacyVariantProduct(product: StoreProduct) {
  return product.variants === undefined && !product.default_variant_id
}
export function enabledStoreVariants(product: StoreProduct): StoreVariant[] {
  return (product.variants || []).filter((variant) => variant.enabled)
}
export function initialStoreVariant(product: StoreProduct): string {
  const variants = enabledStoreVariants(product)
  return variants.length === 1 ? variants[0].id : ''
}
export function selectedStoreVariant(product: StoreProduct, id: string) {
  return enabledStoreVariants(product).find(
    (variant) => variant.id === id && variant.product_id === product.id
  )
}
export function storeVariantPrice(
  product: StoreProduct,
  id: string
): number | undefined {
  const price = legacyVariantProduct(product)
    ? product.price_quota
    : selectedStoreVariant(product, id)?.price_quota
  return Number.isSafeInteger(price) && (price ?? 0) > 0 ? price : undefined
}
export function storeVariantCapacity(
  product: StoreProduct,
  id: string
): number {
  const variant = selectedStoreVariant(product, id)
  if (!legacyVariantProduct(product) && (!variant || variant.trading_paused)) {
    return 0
  }
  // An unbounded capacity is not a stock count. Checkout applies payment,
  // total-price and purchase limits before passing a finite maximum to controls.
  if (storeVariantUnlimitedSupply(product, id) && product.sale_limit == null) {
    return Infinity
  }
  const capacity = legacyVariantProduct(product)
    ? (product.sale_available ?? product.available_stock)
    : variant?.sale_available
  return typeof capacity === 'number' &&
    Number.isSafeInteger(capacity) &&
    capacity >= 0
    ? capacity
    : 0
}

export function storeVariantUnlimitedSupply(product: StoreProduct, id: string) {
  return legacyVariantProduct(product)
    ? product.unlimited_supply === true
    : selectedStoreVariant(product, id)?.unlimited_supply === true
}
