/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import { storePromotionApi } from './promotion-api'
import { verifiedPromotionQuote } from './promotion-utils'
import type { StoreProduct } from './types'
import { legacyVariantProduct, selectedStoreVariant } from './variant-utils'

export function useStoreProductPromotion({
  product,
  variantId,
  quantity,
  code,
  userId,
}: {
  product: StoreProduct
  variantId: string
  quantity: number | undefined
  code: string
  userId?: number
}) {
  const [revision, setRevision] = useState(0)
  const supplied = code.trim()
  const resolve = useQuery({
    queryKey: ['store', 'promotion-resolve', userId, product.id, supplied],
    queryFn: async () => {
      const promotion = await storePromotionApi.resolve(product.id, supplied)
      if (
        promotion.product_id !== product.id ||
        !promotion.code ||
        promotion.status !== 'active'
      ) {
        throw new Error('Store promotion unavailable')
      }
      return promotion
    },
    enabled: !!supplied,
    retry: false,
  })
  const validSelection =
    quantity !== undefined &&
    (legacyVariantProduct(product) ||
      !!selectedStoreVariant(product, variantId))
  const quote = useQuery({
    queryKey: [
      'store',
      'promotion-quote',
      userId,
      product.id,
      resolve.data?.code,
      variantId,
      quantity,
      revision,
    ],
    queryFn: async () => {
      const selection = {
        productId: product.id,
        variantId,
        quantity: quantity!,
        code: resolve.data!.code,
      }
      const result = await storePromotionApi.quote(product.id, {
        promotion_code: selection.code,
        variant_id: variantId,
        quantity: selection.quantity,
      })
      const verified = verifiedPromotionQuote(result, selection)
      if (!verified) throw new Error('Store promotion unavailable')
      return verified
    },
    enabled: !!supplied && !!resolve.data && validSelection,
    retry: false,
  })
  // A quote belongs to one complete selection. Changing a variant or quantity
  // must never keep the previous selection's discounted amount or free state.
  const verified =
    supplied && resolve.data && validSelection && !resolve.error && !quote.error
      ? verifiedPromotionQuote(quote.data, {
          productId: product.id,
          variantId,
          quantity: quantity!,
          code: resolve.data.code,
        })
      : undefined
  return {
    supplied,
    resolved: resolve.data,
    quote: verified,
    pending:
      !!supplied &&
      (resolve.isPending ||
        (validSelection && !!resolve.data && quote.isPending)),
    error: resolve.error || quote.error,
    refresh: () => setRevision((value) => value + 1),
    retry: () => void (resolve.error ? resolve.refetch() : quote.refetch()),
  }
}
