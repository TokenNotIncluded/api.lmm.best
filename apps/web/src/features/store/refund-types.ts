/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StorePaymentMethod } from './types'

export type StoreRefundMode = 'full' | 'quantity' | 'amount'
export type StoreRefundStatus =
  | 'requested'
  | 'awaiting_provider'
  | 'reconciliation_required'
  | 'completed'
  | 'rejected'
  | 'cancelled'

export interface StoreRefundInput {
  request_key: string
  reason: string
  mode: StoreRefundMode
  quantity?: number
  stock_ids?: string[]
  amount_quota?: number
  amount_minor?: number
}

export interface StoreRefund {
  id: string
  order_id: string
  request_key: string
  mode: StoreRefundMode
  quantity: number
  stock_ids: string[]
  amount_quota: number
  amount_minor: number
  currency: string
  reason: string
  status: StoreRefundStatus
  requested_by: number
  requested_role: string
  decision_by: number
  decision_reason: string
  retained_fee_quota: number
  created_at: number
  decided_at: number
  completed_at: number
}

export interface StoreRefundView {
  order_id: string
  product_title: string
  variant_name: string
  payment_method: StorePaymentMethod
  currency: string
  principal_quota: number
  refunded_quota: number
  reserved_quota: number
  remaining_quota: number
  quantity: number
  refunded_quantity: number
  max_quantity: number
  eligible_items: { stock_id: string; position: number }[]
  supports_quantity: boolean
  supports_amount: boolean
  native_basis_verified: boolean
  amount_minor?: number
  refunded_amount_minor?: number
  remaining_amount_minor?: number
  refunds: StoreRefund[]
}

export interface StorePickupRefundProof {
  order_id: string
  token: string
  code?: string
}

export type StoreRefundAudience = 'buyer' | 'seller' | 'root'
