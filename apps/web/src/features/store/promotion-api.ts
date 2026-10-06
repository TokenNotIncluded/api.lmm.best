/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import type {
  StorePromotionAction,
  StorePromotionCode,
  StorePromotionCodeInput,
  StorePromotionQuote,
  StoreResolvedPromotion,
} from './promotion-types'
import type { StorePage } from './types'

type Envelope<T> = { success: boolean; message?: string; data: T }
async function unwrap<T>(request: Promise<{ data: Envelope<T> }>) {
  let response: { data: Envelope<T> }
  try {
    response = await request
  } catch (error) {
    const message = (error as { response?: { data?: { message?: unknown } } })
      ?.response?.data?.message
    throw new Error(
      typeof message === 'string' ? message : 'Store request failed'
    )
  }
  if (response.data.success !== true) {
    throw new Error(response.data.message || 'Store request failed')
  }
  return response.data.data
}
const options = { skipErrorHandler: true, skipBusinessError: true }
const root = (productId: string) =>
  `/api/store/products/${encodeURIComponent(productId)}/promotions`

export const storePromotionApi = {
  list: (productId: string, page = 1) =>
    unwrap<StorePage<StorePromotionCode>>(
      api.get(root(productId), {
        ...options,
        params: { offset: (page - 1) * 20, limit: 20 },
      })
    ),
  create: (productId: string, body: StorePromotionCodeInput) =>
    unwrap<StorePromotionCode>(api.post(root(productId), body, options)),
  update: (productId: string, id: string, body: StorePromotionCodeInput) =>
    unwrap<StorePromotionCode>(
      api.put(`${root(productId)}/${encodeURIComponent(id)}`, body, options)
    ),
  batch: (productId: string, ids: string[], action: StorePromotionAction) =>
    unwrap<{ affected: number }>(
      api.post(`${root(productId)}/batch`, { ids, action }, options)
    ),
  cleanup: (productId: string) =>
    unwrap<{ deleted: number }>(
      api.post(`${root(productId)}/cleanup`, { limit: 100 }, options)
    ),
  resolve: (productId: string, code: string) =>
    unwrap<StoreResolvedPromotion>(
      api.get(`${root(productId)}/resolve`, { ...options, params: { code } })
    ),
  quote: (
    productId: string,
    body: { promotion_code: string; variant_id: string; quantity: number }
  ) =>
    unwrap<StorePromotionQuote>(
      api.post(`${root(productId)}/quote`, body, options)
    ),
}
