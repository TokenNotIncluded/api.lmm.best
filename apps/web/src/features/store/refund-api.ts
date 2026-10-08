/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import type {
  StorePickupRefundProof,
  StoreRefund,
  StoreRefundInput,
  StoreRefundView,
} from './refund-types'

type Envelope<T> = { success: boolean; message?: string; data: T }

export class StoreRefundRequestError extends Error {
  constructor(
    message: string,
    public readonly outcomeUnknown: boolean
  ) {
    super(message)
    this.name = 'StoreRefundRequestError'
  }
}

async function unwrap<T>(
  request: Promise<{ data: Envelope<T> }>,
  mutation = false
): Promise<T> {
  let response: { data: Envelope<T> }
  try {
    response = await request
  } catch (issue) {
    const failure = issue as {
      response?: { status?: number; data?: { message?: string } }
    }
    throw new StoreRefundRequestError(
      failure?.response?.data?.message || 'Refund request failed',
      mutation &&
        (!failure?.response || (failure.response.status ?? 500) >= 500)
    )
  }
  if (response.data.success !== true) {
    throw new StoreRefundRequestError(
      response.data.message || 'Refund request failed',
      false
    )
  }
  return response.data.data
}

const options = {
  skipErrorHandler: true,
  skipBusinessError: true,
  disableDuplicate: true,
}
const pickupOptions = {
  ...options,
  skipAuthRefresh: true,
  withCredentials: true,
}
const root = '/api/store'
const orderRoot = (id: string) =>
  `${root}/orders/${encodeURIComponent(id)}/refunds`

export const storeRefundApi = {
  read: (id: string) =>
    unwrap<StoreRefundView>(api.get(orderRoot(id), options)),
  request: (id: string, input: StoreRefundInput) =>
    unwrap<StoreRefund>(api.post(orderRoot(id), input, options), true),
  proactive: (id: string, input: StoreRefundInput) =>
    unwrap<StoreRefund>(
      api.post(`${orderRoot(id)}/proactive`, input, options),
      true
    ),
  decision: (
    id: string,
    refundId: string,
    decision: 'approve' | 'reject',
    reason: string
  ) =>
    unwrap<StoreRefund>(
      api.post(
        `${orderRoot(id)}/${encodeURIComponent(refundId)}/decision`,
        { decision, reason },
        options
      ),
      true
    ),
  cancel: (id: string, refundId: string) =>
    unwrap<StoreRefund>(
      api.post(
        `${orderRoot(id)}/${encodeURIComponent(refundId)}/cancel`,
        {},
        options
      ),
      true
    ),
  pickupRead: (proof: StorePickupRefundProof) =>
    unwrap<StoreRefundView>(
      api.post(`${root}/pickup/refunds/read`, proof, pickupOptions)
    ),
  pickupRequest: (proof: StorePickupRefundProof, input: StoreRefundInput) =>
    unwrap<StoreRefund>(
      api.post(
        `${root}/pickup/refunds/request`,
        { ...proof, input },
        pickupOptions
      ),
      true
    ),
}
