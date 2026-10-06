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
  StoreGatewayInput,
  StoreGateway,
  StoreProduct,
  StoreProductInput,
  StorePaymentSession,
  StoreStock,
  StorePromotion,
} from './types'

type Envelope<T> = { success: boolean; message?: string; data: T }
async function unwrap<T>(request: Promise<{ data: Envelope<T> }>) {
  let response: { data: Envelope<T> }
  try {
    response = await request
  } catch (error) {
    const body = (error as { response?: { data?: { message?: unknown } } })
      ?.response?.data
    throw new Error(
      typeof body?.message === 'string' ? body.message : 'Store request failed'
    )
  }
  if (response.data.success !== true) {
    throw new Error(response.data.message || 'Store request failed')
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
      'fee_bps' | 'promotion_quota' | 'recipient_id' | 'linuxdo_units_per_usd'
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
}
