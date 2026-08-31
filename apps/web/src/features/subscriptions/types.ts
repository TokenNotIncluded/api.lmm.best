/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { z } from 'zod'

import type {
  WaffoPancakeCheckoutLanguage,
  WaffoPancakeCheckoutRegion,
} from '@/lib/waffo-pancake-checkout'

// ============================================================================
// Subscription Plan Schema & Types
// ============================================================================

export const waffoPancakeProductTypeSchema = z.enum([
  'one_time',
  'subscription',
])

export type WaffoPancakeProductType = z.infer<
  typeof waffoPancakeProductTypeSchema
>

export const subscriptionPlanSchema = z.object({
  id: z.number(),
  title: z.string(),
  subtitle: z.string().optional(),
  price_amount: z.number(),
  currency: z.string().default('USD'),
  duration_unit: z.enum(['year', 'month', 'day', 'hour', 'custom']),
  duration_value: z.number(),
  custom_seconds: z.number().optional(),
  quota_reset_period: z.enum(['never', 'daily', 'weekly', 'monthly', 'custom']),
  quota_reset_custom_seconds: z.number().optional(),
  enabled: z.boolean(),
  archived_at: z.number().optional(),
  sort_order: z.number(),
  allow_balance_pay: z.boolean().optional().default(true),
  allow_wallet_overflow: z.boolean().optional().default(true),
  max_purchase_per_user: z.number(),
  total_amount: z.number(),
  upgrade_group: z.string().optional(),
  downgrade_group: z.string().optional(),
  stripe_price_id: z.string().optional(),
  creem_product_id: z.string().optional(),
  waffo_pancake_product_id: z.string().optional(),
  waffo_pancake_product_type: waffoPancakeProductTypeSchema.optional(),
})

export type SubscriptionPlan = z.infer<typeof subscriptionPlanSchema>

export interface PlanRecord {
  plan: SubscriptionPlan
  /**
   * Public API: checkout methods authorized for this plan and signed-in user.
   * Admin API: configured methods whose gateway credentials are usable.
   */
  payment_methods?: string[]
  /** Server-authoritative platform quota debited for a wallet purchase. */
  balance_price_quota?: number
}

// ============================================================================
// User Subscription Schema & Types
// ============================================================================

export const userSubscriptionSchema = z.object({
  id: z.number(),
  user_id: z.number(),
  plan_id: z.number(),
  status: z.string(),
  source: z.string().optional(),
  start_time: z.number(),
  end_time: z.number(),
  amount_total: z.number(),
  amount_used: z.number(),
  next_reset_time: z.number().optional(),
})

export type UserSubscription = z.infer<typeof userSubscriptionSchema>

export interface UserSubscriptionRecord {
  subscription: UserSubscription
}

// ============================================================================
// API Request/Response Types
// ============================================================================

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export interface PlanPayload {
  plan: Partial<SubscriptionPlan>
}

export interface SubscriptionPayRequest {
  plan_id: number
  payment_method?: string
}

export interface WaffoPancakeSubscriptionPayRequest {
  plan_id: number
  checkout_region?: WaffoPancakeCheckoutRegion
  checkout_language?: WaffoPancakeCheckoutLanguage
}

export interface SubscriptionPayResponse {
  success: boolean
  message?: string
  data?: {
    // Stripe-style hosted checkout link.
    pay_link?: string
    // Waffo Pancake / Creem hosted checkout URL.
    checkout_url?: string
    // Pancake-only: order metadata + self-service buyer session token,
    // surfaced for future flows (refund / cancel from this platform's own UI).
    session_id?: string
    expires_at?: number | string
    order_id?: string
    token?: string
    token_expires_at?: number | string
  }
  url?: string
}

export interface CreateUserSubscriptionRequest {
  plan_id: number
}

export interface SubscriptionPlanRemovalResult {
  action: 'deleted' | 'archived'
  archived_at?: number
  cancelled_orders?: number
}

export interface AdminSubscriptionRecord {
  id: number
  user_id: number
  username: string
  email: string
  plan_id: number
  plan_title: string
  plan_archived_at: number
  amount_total: number
  amount_used: number
  start_time: number
  end_time: number
  status: string
  next_reset_time: number
  source: string
}

export interface AdminSubscriptionRecordPage {
  items: AdminSubscriptionRecord[]
  total: number
  page: number
  page_size: number
}

export interface AdminSubscriptionResetEligible {
  user_id: number
  username: string
  email: string
  plan_id: number
  plan_title: string
  plan_archived_at: number
  active_subscription_count: number
  amount_total: number
  amount_used: number
  next_reset_time: number
  banked_voucher_count: number
}

export interface AdminSubscriptionResetEligiblePage {
  items: AdminSubscriptionResetEligible[]
  total: number
  page: number
  page_size: number
}

export type SubscriptionResetMode = 'hard' | 'soft'

export interface SubscriptionResetTarget {
  user_id: number
  plan_id: number
}

export interface SubscriptionResetFilter {
  query?: string
  plan_ids?: number[]
  user_ids?: number[]
}

export interface SubscriptionResetPreviewRequest {
  mode: SubscriptionResetMode
  all_matching: boolean
  targets?: SubscriptionResetTarget[]
  filter: SubscriptionResetFilter
}

export interface SubscriptionResetPreviewResult {
  token: string
  mode: SubscriptionResetMode
  target_count: number
  user_count: number
  plan_count: number
  active_subscriptions: number
  quota_to_restore: number
  voucher_expires_at: number
  expires_at: number
  targets: AdminSubscriptionResetEligible[]
}

export interface SubscriptionResetExecuteRequest {
  preview_token: string
  operation_id: string
}

export interface SubscriptionResetBatchResult {
  operation_id: string
  mode: SubscriptionResetMode
  requested_targets: number
  processed_targets: number
  skipped_targets: number
  reset_subscriptions: number
  restored_quota: number
  vouchers_issued: number
  voucher_expires_at: number
}

export interface SubscriptionResetVoucher {
  id: number
  user_id: number
  plan_id: number
  plan_title: string
  operation_id: string
  status: 'available' | 'redeemed' | 'expired'
  expired?: boolean
  expires_at: number
  redeemed_at: number
  created_at: number
}

export interface SubscriptionResetResult {
  plan_id: number
  plan_title: string
  matched_count: number
  reset_count: number
  user_count: number
  restored_quota: number
  affected_user_ids: number[]
}

// ============================================================================
// Self Subscription Data (user-facing)
// ============================================================================

export interface SelfSubscriptionData {
  billing_preference: string
  subscriptions: UserSubscriptionRecord[]
  all_subscriptions: UserSubscriptionRecord[]
}

// ============================================================================
// Dialog Types
// ============================================================================

export type SubscriptionsDialogType =
  | 'create'
  | 'update'
  | 'toggle-status'
  | 'delete-plan'
