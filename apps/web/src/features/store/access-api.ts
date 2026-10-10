/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { StoreSellerTerms } from './access-types'
import { StoreAPIError } from './api'
import type { StoreCatalogueProduct } from './catalogue-types'
import type {
  StoreCheckoutInput,
  StoreCheckoutResult,
  StoreOrder,
  StorePaymentSession,
  StoreDisclaimer,
} from './types'

type Envelope<T> = {
  order_id?: string
  order_status?: string
  order_cancelled?: boolean
  request_key?: string
  order_created?: boolean
  success: boolean
  code?: string
  message?: string
  data: T
}
export class StoreAccessRequestError extends StoreAPIError {
  constructor(
    body: ConstructorParameters<typeof StoreAPIError>[0],
    public readonly httpStatus?: number
  ) {
    super(body)
    this.name = 'StoreAccessRequestError'
  }
}
async function unwrap<T>(request: Promise<{ data: Envelope<T> }>): Promise<T> {
  let response: { data: Envelope<T> }
  try {
    response = await request
  } catch (issue) {
    const response = (
      issue as { response?: { status?: number; data?: Envelope<T> } }
    )?.response
    throw new StoreAccessRequestError(response?.data, response?.status)
  }
  if (response.data.success !== true) {
    throw new StoreAccessRequestError(response.data, 200)
  }
  return response.data.data
}
const options = {
  skipErrorHandler: true,
  skipBusinessError: true,
  disableDuplicate: true,
}
export type StoreCapturedAuthScope = {
  userId: number | undefined
  sessionId: string | undefined
}
function capturedAuthScope() {
  const auth = useAuthStore.getState().auth
  return { userId: auth.user?.id, sessionId: auth.session?.sid }
}
export const storeAccessApi = {
  myTerms: () =>
    unwrap<StoreSellerTerms>(
      api.get('/api/store/my/terms', {
        ...options,
        authScope: capturedAuthScope(),
      })
    ),
  saveTerms: (content: string, expectedVersion: string) =>
    unwrap<StoreSellerTerms>(
      api.put(
        '/api/store/my/terms',
        { content, expected_version: expectedVersion },
        { ...options, authScope: capturedAuthScope() }
      )
    ),
  productTerms: (id: string, guestToken?: string) =>
    unwrap<StoreSellerTerms>(
      api.get(`/api/store/products/${encodeURIComponent(id)}/terms`, {
        ...options,
        skipAuthRefresh: true,
        authScope: guestToken
          ? {
              userId: undefined,
              sessionId: useAuthStore.getState().auth.session?.sid,
            }
          : capturedAuthScope(),
        ...(guestToken ? { headers: { 'X-Store-Guest': guestToken } } : {}),
      })
    ),
}

export interface StoreGuestSession {
  token: string
  expires_at: number
  guest_id: string
}
export const storeGuestRequestOptions = (
  token: string,
  scope?: StoreCapturedAuthScope
) => ({
  ...options,
  skipAuthRefresh: true,
  authScope: scope ?? {
    userId: undefined,
    sessionId: useAuthStore.getState().auth.session?.sid,
  },
  headers: { 'X-Store-Guest': token },
})
const guestOrderPath = (id: string) =>
  `/api/store/guest/orders/${encodeURIComponent(id)}`
export const storeGuestApi = {
  product: (token: string, id: string, signal?: AbortSignal) =>
    unwrap<StoreCatalogueProduct>(
      api.get(`/api/store/products/${encodeURIComponent(id)}`, {
        ...storeGuestRequestOptions(token),
        signal,
      })
    ),
  session: () =>
    unwrap<StoreGuestSession>(
      api.post(
        '/api/store/guest/session',
        {},
        {
          ...options,
          skipAuthRefresh: true,
          authScope: {
            userId: undefined,
            sessionId: useAuthStore.getState().auth.session?.sid,
          },
        }
      )
    ),
  checkout: (
    token: string,
    body: StoreCheckoutInput,
    scope?: StoreCapturedAuthScope
  ) =>
    unwrap<StoreCheckoutResult>(
      api.post(
        '/api/store/guest/orders',
        body,
        storeGuestRequestOptions(token, scope)
      )
    ),
  order: (token: string, id: string, scope?: StoreCapturedAuthScope) =>
    unwrap<StoreOrder>(
      api.get(guestOrderPath(id), storeGuestRequestOptions(token, scope))
    ),
  lookup: async (
    token: string,
    requestKey: string,
    scope?: StoreCapturedAuthScope
  ) => {
    try {
      return await unwrap<StoreOrder>(
        api.post(
          '/api/store/guest/orders/lookup',
          { request_key: requestKey },
          storeGuestRequestOptions(token, scope)
        )
      )
    } catch (issue) {
      if (
        issue instanceof StoreAccessRequestError &&
        issue.httpStatus === 404
      ) {
        return null
      }
      throw issue
    }
  },
  disclaimer: (token: string) =>
    unwrap<StoreDisclaimer>(
      api.get('/api/store/disclaimer', storeGuestRequestOptions(token))
    ),
  acceptDisclaimer: (token: string, version: string) =>
    unwrap<unknown>(
      api.post(
        '/api/store/guest/disclaimer/accept',
        { version, accepted: true },
        storeGuestRequestOptions(token)
      )
    ),
  memberLookup: async (requestKey: string, scope?: StoreCapturedAuthScope) => {
    try {
      return await unwrap<StoreOrder>(
        api.get(
          `/api/store/orders/by-request-key/${encodeURIComponent(requestKey)}`,
          { ...options, authScope: scope ?? capturedAuthScope() }
        )
      )
    } catch (issue) {
      if (
        issue instanceof StoreAccessRequestError &&
        issue.httpStatus === 404
      ) {
        return null
      }
      throw issue
    }
  },
  pay: (
    token: string,
    id: string,
    currency?: string,
    scope?: StoreCapturedAuthScope
  ) =>
    unwrap<StorePaymentSession>(
      api.post(
        `${guestOrderPath(id)}/pay`,
        currency ? { currency } : {},
        storeGuestRequestOptions(token, scope)
      )
    ),
  cancel: (token: string, id: string) =>
    unwrap<null>(
      api.post(
        `${guestOrderPath(id)}/cancel`,
        {},
        storeGuestRequestOptions(token)
      )
    ),
  reconcile: (token: string, id: string, scope?: StoreCapturedAuthScope) =>
    unwrap<StoreOrder>(
      api.post(
        `${guestOrderPath(id)}/reconcile`,
        {},
        storeGuestRequestOptions(token, scope)
      )
    ),
  pickupLink: (token: string, id: string) =>
    unwrap<{ pickup_url: string }>(
      api.get(
        `${guestOrderPath(id)}/pickup-link`,
        storeGuestRequestOptions(token)
      )
    ),
}

export const storeGuestEmailApi = {
  status: (token: string, email: string) =>
    unwrap<{ verified: boolean }>(
      api.post(
        '/api/store/guest/email/status',
        { email },
        storeGuestRequestOptions(token)
      )
    ),
  send: (token: string, email: string) =>
    unwrap<{ sent: boolean; challenge_id: string; expires_at: number }>(
      api.post(
        '/api/store/guest/email/verification/send',
        { email },
        storeGuestRequestOptions(token)
      )
    ),
  confirm: (token: string, email: string, challenge_id: string, code: string) =>
    unwrap<{ verified: boolean }>(
      api.post(
        '/api/store/guest/email/verification/confirm',
        { email, challenge_id, code },
        storeGuestRequestOptions(token)
      )
    ),
}
