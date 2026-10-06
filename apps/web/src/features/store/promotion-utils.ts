/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StorePromotionQuote } from './promotion-types'

export function promotionDiscountBps(value: string): number | undefined {
  if (!/^\d{1,3}(?:\.\d{1,2})?$/.test(value)) return undefined
  const [whole, fraction = ''] = value.split('.')
  const bps = Number(whole) * 100 + Number(fraction.padEnd(2, '0'))
  return bps <= 10000 ? bps : undefined
}

export function promotionShareUrl(
  productId: string,
  code: string,
  ownerPreview = false
) {
  const url = new URL(
    `/store/${ownerPreview ? 'preview' : 'products'}/${encodeURIComponent(productId)}`,
    window.location.origin
  )
  url.searchParams.set('promotion', code)
  return url.href
}

export function verifiedPromotionQuote(
  quote: StorePromotionQuote | undefined,
  selection: {
    productId: string
    variantId: string
    quantity: number
    code: string
  }
): StorePromotionQuote | undefined {
  if (
    !quote ||
    typeof quote.checkout_allowed !== 'boolean' ||
    !Number.isSafeInteger(quote.max_quantity) ||
    quote.max_quantity < 0 ||
    quote.max_quantity > 1000 ||
    (quote.checkout_allowed && quote.quantity > quote.max_quantity) ||
    quote.product_id !== selection.productId ||
    quote.variant_id !== selection.variantId ||
    quote.quantity !== selection.quantity ||
    quote.promotion_code !== selection.code ||
    ![
      quote.original_price_quota,
      quote.discount_quota,
      quote.price_quota,
      quote.discount_bps,
    ].every((value) => Number.isSafeInteger(value) && value >= 0) ||
    quote.discount_bps > 10000 ||
    quote.original_price_quota - quote.discount_quota !== quote.price_quota ||
    quote.free !== (quote.price_quota === 0) ||
    (quote.free && quote.discount_bps !== 10000) ||
    !Array.isArray(quote.payment_methods)
  ) {
    return undefined
  }
  return quote
}
