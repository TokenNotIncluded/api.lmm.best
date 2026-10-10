/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StorePaymentMethod } from './types'

export type StorePromotionStatus = 'active' | 'paused' | 'revoked'
export type StorePromotionAction = 'pause' | 'resume' | 'revoke' | 'delete'
export interface StorePromotionCodeInput {
  discount_bps: number
  variant_ids: string[]
  expires_at: number | null
  max_uses: number | null
  status: StorePromotionStatus
}
export interface StorePromotionCode extends StorePromotionCodeInput {
  id: string
  product_id: string
  seller_id: number
  code: string
  uses_count: number
  reserved_count: number
  created_at: number
  updated_at: number
  share_path: string
}
export interface StoreResolvedPromotion {
  product_id: string
  code: string
  discount_bps: number
  variant_ids: string[]
  expires_at: number | null
  status: StorePromotionStatus
}
export interface StorePromotionQuote {
  product_id: string
  variant_id: string
  quantity: number
  promotion_code: string
  discount_bps: number
  original_price_quota: number
  discount_quota: number
  price_quota: number
  free: boolean
  checkout_allowed: boolean
  max_quantity: number
  payment_methods: (StorePaymentMethod | 'free')[]
}
