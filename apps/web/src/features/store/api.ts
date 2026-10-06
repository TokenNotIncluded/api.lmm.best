/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import type {
  StoreCheckoutInput,
  StoreCheckoutResult,
  StoreClaim,
  StoreClaimMetadata,
  StoreConfig,
  StoreDisclaimer,
  StoreOrder,
  StorePage,
  StorePaymentSettings,
  StorePaymentCategories,
  StoreGatewayInput,
  StoreGateway,
  StoreProduct,
  StoreVariant,
  StoreVariantInput,
  StoreProductInput,
  StorePaymentSession,
  StoreStock,
  StorePromotion,
  StoreOrderSummary,
} from './types'

type Envelope<T> = {
  success: boolean
  code?: string
  message?: string
  data: T
}
function errorMessage(body?: { code?: unknown; message?: unknown }) {
  // Known codes have stable localized copy; arbitrary server messages retain
  // their original meaning instead of being guessed from HTTP status.
  if (body?.code === 'STORE_VARIANT_REQUIRED') {
    return 'Choose a variant before ordering.'
  }
  if (body?.code === 'STORE_UPGRADE_IN_PROGRESS') {
    return 'Shop upgrade is in progress. Existing orders are still accessible.'
  }
  return typeof body?.message === 'string' && body.message
    ? body.message
    : 'Store request failed'
}
async function unwrap<T>(request: Promise<{ data: Envelope<T> }>) {
  let response: { data: Envelope<T> }
  try {
    response = await request
  } catch (error) {
    const body = (
      error as { response?: { data?: { code?: unknown; message?: unknown } } }
    )?.response?.data
    throw new Error(errorMessage(body))
  }
  if (response.data.success !== true) {
    throw new Error(errorMessage(response.data))
  }
  return response.data.data
}
const options = { skipErrorHandler: true, skipBusinessError: true }
const root = '/api/store'
// Cold-open collection authenticates only this order using the existing
// HttpOnly cookie. Never refresh a broader session or redirect from this page.
// An existing bearer remains attached and takes precedence on the server.
const claimRoot = '/api/user/auth/store-claim'
const claimOptions = {
  ...options,
  skipAuthRefresh: true,
  disableDuplicate: true,
  withCredentials: true,
}
export const storeApi = {
  config: () => unwrap<StoreConfig>(api.get(`${root}/config`, options)),
  products: (search = '', page = 1) =>
    unwrap<StorePage<StoreProduct>>(
      api.get(`${root}/products`, {
        ...options,
        params: { q: search, offset: (page - 1) * 24, limit: 24 },
      })
    ),
  product: (id: string) =>
    unwrap<StoreProduct>(api.get(`${root}/products/${id}`, options)),
  previewProduct: (id: string) =>
    unwrap<StoreProduct>(api.get(`${root}/my/products/${id}/preview`, options)),
  myProducts: (page = 1) =>
    unwrap<StorePage<StoreProduct>>(
      api.get(`${root}/my/products`, {
        ...options,
        params: { offset: (page - 1) * 20, limit: 20 },
      })
    ),
  createProduct: (body: StoreProductInput) =>
    unwrap<StoreProduct>(api.post(`${root}/products`, body, options)),
  updateProduct: (id: string, body: StoreProductInput) =>
    unwrap<StoreProduct>(api.put(`${root}/products/${id}`, body, options)),
  submitProduct: (id: string) =>
    unwrap<null>(api.post(`${root}/products/${id}/submit`, {}, options)),
  pauseProduct: (id: string, paused: boolean) =>
    unwrap<null>(api.put(`${root}/products/${id}/paused`, { paused }, options)),
  saleLimit: (id: string, sale_limit: number | null) =>
    unwrap<null>(
      api.put(`${root}/products/${id}/sale-limit`, { sale_limit }, options)
    ),
  listing: (id: string, listed: boolean) =>
    unwrap<null>(
      api.put(`${root}/products/${id}/listing`, { listed }, options)
    ),
  inventory: (id: string, items: string[]) =>
    unwrap<{ added: number }>(
      api.post(`${root}/products/${id}/inventory`, { items }, options)
    ),
  stock: (id: string, page = 1) =>
    unwrap<StorePage<StoreStock>>(
      api.get(`${root}/products/${id}/inventory`, {
        ...options,
        params: { offset: (page - 1) * 20, limit: 20 },
      })
    ),
  removeStock: (id: string, stockId: string) =>
    unwrap<null>(
      api.delete(`${root}/products/${id}/inventory/${stockId}`, options)
    ),
  createVariant: (id: string, body: StoreVariantInput) =>
    unwrap<StoreVariant>(
      api.post(`${root}/products/${id}/variants`, body, options)
    ),
  updateVariant: (id: string, variantId: string, body: StoreVariantInput) =>
    unwrap<StoreVariant>(
      api.put(`${root}/products/${id}/variants/${variantId}`, body, options)
    ),
  enableVariant: (id: string, variantId: string, enabled: boolean) =>
    unwrap<StoreVariant>(
      api.put(
        `${root}/products/${id}/variants/${variantId}/enabled`,
        { enabled },
        options
      )
    ),
  variantStock: (id: string, variantId: string, page = 1) =>
    unwrap<StorePage<StoreStock>>(
      api.get(`${root}/products/${id}/variants/${variantId}/inventory`, {
        ...options,
        params: { offset: (page - 1) * 20, limit: 20 },
      })
    ),
  importVariantStock: (id: string, variantId: string, items: string[]) =>
    unwrap<{ added: number }>(
      api.post(
        `${root}/products/${id}/variants/${variantId}/inventory`,
        { items },
        options
      )
    ),
  removeVariantStock: (id: string, variantId: string, stockId: string) =>
    unwrap<null>(
      api.delete(
        `${root}/products/${id}/variants/${variantId}/inventory/${stockId}`,
        options
      )
    ),
  promoteProduct: (id: string, request_key: string) =>
    unwrap<StorePromotion>(
      api.post(
        `${root}/products/${id}/promotion`,
        { months: 1, request_key },
        options
      )
    ),
  orders: (role: 'buyer' | 'seller', page = 1) =>
    unwrap<StorePage<StoreOrder>>(
      api.get(`${root}/my/orders`, {
        ...options,
        params: { role, offset: (page - 1) * 20, limit: 20 },
      })
    ),
  order: (id: string) =>
    unwrap<StoreOrder>(api.get(`${root}/orders/${id}`, options)),
  checkout: (body: StoreCheckoutInput) =>
    unwrap<StoreCheckoutResult>(api.post(`${root}/orders`, body, options)),
  pay: (id: string, currency?: string) =>
    unwrap<StorePaymentSession>(
      api.post(
        `${root}/orders/${id}/pay`,
        currency ? { currency } : {},
        options
      )
    ),
  cancel: (id: string) =>
    unwrap<null>(api.post(`${root}/orders/${id}/cancel`, {}, options)),
  reconcile: (id: string) =>
    unwrap<unknown>(api.post(`${root}/orders/${id}/reconcile`, {}, options)),
  pickupLink: (id: string) =>
    unwrap<{ pickup_url: string }>(
      api.get(`${root}/orders/${id}/pickup-link`, options)
    ),
  disclaimer: () =>
    unwrap<StoreDisclaimer>(api.get(`${root}/disclaimer`, options)),
  acceptDisclaimer: (version: string) =>
    unwrap<null>(
      api.post(
        `${root}/disclaimer/accept`,
        { version, accepted: true },
        options
      )
    ),
  paymentSettings: () =>
    unwrap<StorePaymentSettings>(api.get(`${root}/payments/settings`, options)),
  savePaymentSettings: (body: StoreGatewayInput) =>
    unwrap<StoreGateway>(api.put(`${root}/payments/settings`, body, options)),
  savePaymentCategories: (body: StorePaymentCategories) =>
    unwrap<StorePaymentCategories>(
      api.put(`${root}/payments/categories`, body, options)
    ),
  claimMetadata: (token: string) =>
    unwrap<StoreClaimMetadata>(
      api.get(`${claimRoot}/${encodeURIComponent(token)}`, claimOptions)
    ),
  claim: (token: string, pickup_code: string) =>
    unwrap<StoreClaim>(
      api.post(
        `${claimRoot}/${encodeURIComponent(token)}`,
        { pickup_code },
        claimOptions
      )
    ),
  reviews: (page = 1) =>
    unwrap<StorePage<StoreProduct>>(
      api.get(`${root}/reviews`, {
        ...options,
        params: { offset: (page - 1) * 20, limit: 20 },
      })
    ),
  review: (id: string, approved: boolean, note: string) =>
    unwrap<null>(
      api.post(
        `${root}/products/${id}/review`,
        { approve: approved, note },
        options
      )
    ),
  saveConfig: (
    body: Pick<
      StoreConfig,
      | 'fee_bps'
      | 'promotion_quota'
      | 'minimum_unit_price_quota'
      | 'recipient_id'
      | 'linuxdo_units_per_usd'
    >
  ) => unwrap<null>(api.put(`${root}/config`, body, options)),
  savePromotionPrice: (promotion_quota: number) =>
    unwrap<null>(
      api.put(`${root}/promotion-config`, { promotion_quota }, options)
    ),
  deliveryEmailStatus: () =>
    unwrap<{ verified: boolean; email?: string }>(
      api.get(`${root}/email/status`, options)
    ),
  sendDeliveryEmailVerification: () =>
    unwrap<{ sent: boolean }>(
      api.post(`${root}/email/verification/send`, {}, options)
    ),
  confirmDeliveryEmailVerification: (code: string) =>
    unwrap<null>(
      api.post(`${root}/email/verification/confirm`, { code }, options)
    ),
  searchOrder: (tradeNo: string) =>
    unwrap<StoreOrderSummary>(
      api.get(`${root}/order-search/${encodeURIComponent(tradeNo)}`, options)
    ),
  sendOrderSearchEmailCode: (email: string) =>
    unwrap<{ challenge_id: string; expires_in: number; resend_after: number }>(
      api.post(`${root}/order-search/email/send`, { email }, options)
    ),
  confirmOrderSearchEmailCode: (challenge_id: string, code: string) =>
    unwrap<{ search_token: string; expires_in: number }>(
      api.post(
        `${root}/order-search/email/confirm`,
        { challenge_id, code },
        options
      )
    ),
  searchOrdersByVerifiedEmail: (search_token: string, offset = 0) =>
    unwrap<StorePage<StoreOrderSummary>>(
      api.post(
        `${root}/order-search`,
        { search_token, offset, limit: 20 },
        options
      )
    ),
}
