/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type {
  StoreCatalogueProduct,
  StoreCatalogueTag,
} from './catalogue-types'

export type StoreStockTagProduct = Pick<StoreCatalogueProduct, 'display_tags'> &
  Partial<
    Pick<
      StoreCatalogueProduct,
      | 'available_stock'
      | 'inventory_available'
      | 'sale_available'
      | 'sale_limit'
      | 'unlimited_supply'
      | 'trading_paused'
      | 'variants'
    >
  >

const isPositiveCount = (value: number | undefined) =>
  Number.isSafeInteger(value) && (value ?? 0) > 0

function stockTag(
  product: StoreStockTagProduct
): StoreCatalogueTag | undefined {
  // Older APIs may expose only derived tags. Keep those authoritative when
  // no purchase-availability facts are supplied.
  if (
    product.sale_available === undefined &&
    product.unlimited_supply !== true &&
    product.trading_paused !== true
  ) {
    return undefined
  }
  const available = product.sale_available
  if (
    !product.trading_paused &&
    (isPositiveCount(available) ||
      (product.unlimited_supply === true && product.sale_limit == null))
  ) {
    return 'in_stock'
  }
  if (product.trading_paused) {
    const hasSupply =
      product.variants !== undefined
        ? product.variants.some(
            (variant) =>
              variant.enabled &&
              (variant.unlimited_supply === true ||
                isPositiveCount(variant.inventory_available))
          )
        : product.unlimited_supply === true ||
          isPositiveCount(product.inventory_available) ||
          isPositiveCount(product.available_stock)
    if (hasSupply) {
      return 'trading_paused'
    }
  }
  return 'out_of_stock'
}

export function storeProductDisplayTags(
  product: StoreStockTagProduct
): StoreCatalogueTag[] {
  const tag = stockTag(product)
  // Correct only a server-provided stock classification, not seller-defined
  // tags or unproven availability. Physical inventory never enables checkout.
  return [
    ...new Set(
      (product.display_tags ?? []).map((current) =>
        tag &&
        (current === 'in_stock' ||
          current === 'out_of_stock' ||
          current === 'trading_paused')
          ? tag
          : current
      )
    ),
  ]
}
