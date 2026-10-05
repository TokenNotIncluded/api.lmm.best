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
// ============================================================================
// Wallet Type Definitions
// ============================================================================

import type {
  WaffoPancakeCheckoutLanguage,
  WaffoPancakeCheckoutRegion,
} from '@/lib/waffo-pancake-checkout'

/**
 * Generic API response
 */
export interface ApiResponse<T = unknown> {
  success?: boolean
  message?: string
  data?: T
}

/**
 * Standard API response types
 */
export type TopupInfoResponse = ApiResponse<TopupInfo>
export type RedemptionResponse = ApiResponse<number>
export interface CreditUnitMetadata {
  credit_unit_schema_version: number
  quota_unit: string
  public_credit_unit: string
  legacy_credit_unit: string
  ledger_quota_per_usd: number
  ledger_quota_per_usd_exact: string
  public_credits_per_usd: number
  public_credits_per_usd_exact: string
}

export interface CreditAmountMetadata extends Partial<CreditUnitMetadata> {
  credited_quota?: number
  /** Compatibility alias; this is immutable raw ledger quota. */
  credit_amount?: number
  credit_amount_unit?: 'LEDGER_QUOTA'
  public_credit_amount?: string
  public_credit_amount_unit?: 'CREDIT'
  public_credit_metadata_version?: number
}

export type AmountResponse = ApiResponse<string> &
  CreditAmountMetadata & {
    settlement_currency?: string
    /** Optional same-currency breakdown; validate its version and basis before display. */
    settlement_quote?: unknown
    original_settlement_amount?: string
    savings_settlement_amount?: string
    amount_unit?: 'LEGACY' | 'USD' | 'CNY' | 'CREDIT' | 'LEDGER_QUOTA'
    legacy_batch_units?: string
  }
export type DiscountCodeResponse = ApiResponse<{
  code: string
  discount_percent: number
  min_amount: number
}>
export type PaymentResponse = ApiResponse<Record<string, unknown>> & {
  url?: string
}
export type StripePaymentResponse = ApiResponse<{ pay_link: string }>
export type AffiliateCodeResponse = ApiResponse<string>
export type AffiliateInvitationResponse = ApiResponse
export type AffiliateTransferResponse = ApiResponse
export type CreemPaymentResponse = ApiResponse<{ checkout_url: string }>
export type WaffoPaymentResponse = ApiResponse<
  { payment_url?: string } | string
>
export type WaffoPancakePaymentResponse = ApiResponse<
  | {
      checkout_url?: string
      session_id?: string
      expires_at?: number | string
      order_id?: string
      settlement_amount?: string
      settlement_currency?: 'CNY' | 'USD'
      // Self-service session token + expiry — surfaced by the backend so
      // future flows (refund / cancel from this platform's own UI) can use them
      // without re-issuing checkout. Not consumed by the current handler.
      token?: string
      token_expires_at?: number | string
    }
  | string
>

/**
 * Creem product configuration
 */
export interface CreemProduct {
  /** Product display name */
  name: string
  /** Creem product ID */
  productId: string
  /** Product price */
  price: number
  /** Quota amount to credit */
  quota: number
  /** Currency (USD or EUR) */
  currency: 'USD' | 'EUR'
}

/**
 * Creem payment request
 */
export interface AffiliateInvitationRequest {
  /** Recipient address; the backend constructs the trusted affiliate URL. */
  email: string
}

export interface CreemPaymentRequest {
  /** Creem product ID */
  product_id: string
  /** Payment method identifier */
  payment_method: 'creem'
}

/**
 * Payment method configuration
 */
export interface PaymentMethod {
  /** Display name of payment method */
  name: string
  /** Payment method type identifier */
  type: string
  /** Legacy optional color for UI display */
  color?: string
  /** Optional administrator-provided instructions shown on the selector. */
  description?: string
  /** Server-normalized legacy batch policy, independent of display units. */
  min_topup_credit?: string | number
  max_topup_credit?: string | number
  min_topup_ledger_quota?: string
  max_topup_ledger_quota?: string
  min_topup_public_credit?: string
  max_topup_public_credit?: string
  credit_amount_unit?: 'LEDGER_QUOTA'
  legacy_min_topup?: string | number
  legacy_max_topup_amount?: string | number
  min_topup_unit?: 'USD' | 'LEGACY'
  max_topup_amount_unit?: 'CREDIT' | 'LEGACY'
  /** Minimum topup amount for this payment method */
  min_topup?: number
  /** Maximum credited USD allowed in one payment for this method. */
  max_topup?: string | number
  /** Server-derived maximum in the same units as the top-up request amount. */
  max_topup_amount?: string | number
  /** Optional react-icons component name or safe icon URL */
  icon?: string
  /** Explicit ISO/code unit charged by the gateway, for example USD or CNY. */
  settlement_currency?: string
  /** Legacy recharge batches represented by 1 real USD in the settlement contract. */
  platform_units_per_usd?: string | number
  /** Gateway settlement units represented by 1 real USD. */
  settlement_units_per_usd?: string | number
  /** Explicit direct rate for legacy gateways that do not use the USD bridge. */
  settlement_units_per_platform_unit?: string | number
  /** @deprecated Legacy gateway settlement unit; use settlement_currency. */
  settlement_unit?: string
  /**
   * @deprecated Legacy settlement units per platform unit. Kept only as an
   * explicit compatibility fallback when the two USD-based rates are absent.
   */
  unit_price?: string | number
  /** Per-method payment multiplier combined with the user's group multiplier. */
  topup_ratio?: string | number
}

/**
 * Waffo payment method configuration
 */
export interface WaffoPayMethod {
  /** Display name of payment method */
  name: string
  /** Optional icon path */
  icon?: string
  /** Waffo pay method type */
  payMethodType?: string
  /** Waffo pay method name */
  payMethodName?: string
}

/**
 * Topup configuration information
 */
export interface TopupInfo extends Partial<CreditUnitMetadata> {
  /** Internal compatibility catalog label; CREDIT here retains raw ledger integers. */
  amount_unit?: 'LEGACY' | 'CREDIT'
  credit_metadata_available?: boolean
  credit_metadata_version?: number
  public_credit_metadata_version?: number
  public_credit_amount_unit?: 'CREDIT'
  ledger_quota_amount_options?: number[]
  public_credit_amount_options?: string[]
  ledger_quota_discount?: Record<string, number>
  public_credit_discount?: Record<string, number>
  ledger_quota_min_topup?: number
  stripe_ledger_quota_min_topup?: number
  waffo_ledger_quota_min_topup?: number
  pancake_ledger_quota_min_topup?: number
  stripe_ledger_quota_max_topup?: number | null
  waffo_ledger_quota_max_topup?: number | null
  pancake_ledger_quota_max_topup?: number | null
  public_credit_min_topup?: string
  stripe_public_credit_min_topup?: string
  waffo_public_credit_min_topup?: string
  pancake_public_credit_min_topup?: string
  stripe_public_credit_max_topup?: string | null
  waffo_public_credit_max_topup?: string | null
  pancake_public_credit_max_topup?: string | null
  credit_amount_options?: number[]
  credit_discount?: Record<number, number>
  credit_min_topup?: number
  stripe_credit_min_topup?: number
  waffo_credit_min_topup?: number
  pancake_credit_min_topup?: number
  /** Complete provider caps; explicit null means no configured cap. */
  stripe_credit_max_topup?: number | null
  waffo_credit_max_topup?: number | null
  pancake_credit_max_topup?: number | null
  legacy_amount_unit?: 'LEGACY'
  legacy_amount_options?: number[]
  legacy_discount?: Record<number, number>
  /** Whether this account has completed the paid developer-access activation. */
  developer_access_granted?: boolean
  /** Whether activation is required before normal console access. */
  activation_required?: boolean
  /** Whether at least one activation payment path is configured. */
  payment_available?: boolean
  /** Whether online topup is enabled */
  enable_online_topup: boolean
  /** Whether Stripe topup is enabled */
  enable_stripe_topup: boolean
  /** Available payment methods */
  pay_methods: PaymentMethod[]
  /** Minimum topup amount for online topup */
  min_topup: number
  /** Minimum topup amount for Stripe */
  stripe_min_topup: number
  /** Preset amount options */
  amount_options: number[]
  /** Discount rates by amount */
  discount: Record<number, number>
  /** Top-up pricing multiplier for the current user's group. */
  topup_group_ratio?: number
  /** Optional topup link for purchasing codes */
  topup_link?: string
  /** Whether Creem topup is enabled */
  enable_creem_topup?: boolean
  /** Available Creem products */
  creem_products?: CreemProduct[]
  /** Whether Waffo topup is enabled */
  enable_waffo_topup?: boolean
  /** Fiat settlement currency used by Waffo. */
  waffo_currency?: string
  /** Fiat amount charged for one legacy recharge batch by Waffo. */
  waffo_unit_price?: number | string
  /** Available Waffo payment methods */
  waffo_pay_methods?: WaffoPayMethod[]
  /** Minimum topup amount for Waffo */
  waffo_min_topup?: number
  /** Whether Waffo Pancake topup is enabled */
  enable_waffo_pancake_topup?: boolean
  /** Account-selected currency; payable amounts still require a paired server quote. */
  waffo_pancake_currency?: 'CNY' | 'USD'
  /** Whether plan-level Stripe checkout is enabled */
  enable_stripe_subscription?: boolean
  /** Whether plan-level Creem checkout is enabled */
  enable_creem_subscription?: boolean
  /** Whether plan-level Waffo Pancake checkout is enabled */
  enable_waffo_pancake_subscription?: boolean
  /** Minimum topup amount for Waffo Pancake */
  waffo_pancake_min_topup?: number
  /** Whether redemption code usage is enabled */
  enable_redemption?: boolean
  /** Whether compliance confirmation has been completed */
  payment_compliance_confirmed?: boolean
  /** Current compliance terms version */
  payment_compliance_terms_version?: string
}

/**
 * Preset amount option with optional discount
 */
export interface PresetAmount {
  /** Raw integer Credit value after top-up metadata normalization. */
  value: number
  /** Optional discount rate (0-1) */
  discount?: number
}

/**
 * Redemption code request
 */
export interface RedemptionRequest {
  /** Redemption code key */
  key: string
}

/**
 * Payment request parameters
 */
export interface PaymentRequest {
  /** Explicit legacy batch unit; display currency never changes this request. */
  amount_unit?: 'LEGACY'
  /** Topup amount */
  amount: number
  /** Payment method identifier */
  payment_method: string
  /** Optional administrator-issued percentage discount code. */
  discount_code?: string
}

/**
 * Waffo payment request parameters
 */
export interface WaffoPaymentRequest {
  /** Explicit legacy batch unit; display currency never changes this request. */
  amount_unit?: 'LEGACY'
  /** Topup amount */
  amount: number
  /** Optional server-side Waffo payment method index */
  pay_method_index?: number
  discount_code?: string
}

/**
 * Waffo Pancake payment request parameters
 */
export interface WaffoPancakePaymentRequest {
  /** Explicit legacy batch unit; display currency never changes this request. */
  amount_unit?: 'LEGACY'
  settlement_currency?: 'CNY' | 'USD'
  settlement_amount?: string
  /** Topup amount */
  amount: number
  /** Waffo Pancake checkout region selected by the user or derived from locale */
  checkout_region?: WaffoPancakeCheckoutRegion
  /** Waffo Pancake checkout language derived from the interface locale */
  checkout_language?: WaffoPancakeCheckoutLanguage
  discount_code?: string
}

/**
 * Amount calculation request
 */
export interface AmountRequest {
  /** Explicit legacy batch unit; display currency never changes this request. */
  amount_unit?: 'LEGACY'
  /** Topup amount to calculate */
  amount: number
  /** Gateway selected for a regular Epay amount calculation. */
  payment_method?: string
  /** Optional administrator-issued percentage discount code. */
  discount_code?: string
}

/** Historical Credit* callers supply raw ledger integers, never public CREDIT projections. */
export interface LedgerQuotaRequestUnit {
  amount_unit: 'LEDGER_QUOTA'
  credit_metadata_version: 2
  expected_public_credits_per_usd_exact?: never
}
export interface PublicCreditRequestUnit {
  amount_unit: 'CREDIT'
  credit_metadata_version: 2
  expected_public_credits_per_usd_exact: string
}
export type CreditWireRequestUnit =
  | {
      amount_unit?: 'CREDIT'
      credit_metadata_version?: never
      expected_public_credits_per_usd_exact?: never
    }
  | LedgerQuotaRequestUnit
  | PublicCreditRequestUnit

export type CreditAmountRequest = Omit<AmountRequest, 'amount_unit'> & {
  amount_unit?: 'CREDIT'
}
export type CreditPaymentRequest = Omit<PaymentRequest, 'amount_unit'> & {
  amount_unit?: 'CREDIT'
}
export type CreditWaffoPaymentRequest = Omit<
  WaffoPaymentRequest,
  'amount_unit'
> & { amount_unit?: 'CREDIT' }

export type VersionedCreditAmountRequest = Omit<AmountRequest, 'amount_unit'> &
  (LedgerQuotaRequestUnit | PublicCreditRequestUnit)
export type VersionedCreditPaymentRequest = Omit<
  PaymentRequest,
  'amount_unit'
> &
  (LedgerQuotaRequestUnit | PublicCreditRequestUnit)
export type VersionedCreditWaffoPaymentRequest = Omit<
  WaffoPaymentRequest,
  'amount_unit'
> &
  (LedgerQuotaRequestUnit | PublicCreditRequestUnit)
export type VersionedCreditPancakePaymentRequest = Omit<
  WaffoPancakePaymentRequest,
  'amount_unit'
> &
  (LedgerQuotaRequestUnit | PublicCreditRequestUnit)
export type CreditPancakePaymentRequest = Omit<
  WaffoPancakePaymentRequest,
  'amount_unit'
> & { amount_unit?: 'CREDIT' }

/**
 * Affiliate quota transfer request
 */
export interface AffiliateTransferRequest {
  /** Quota amount to transfer */
  quota: number
}

/**
 * User wallet data
 */
export interface UserWalletData {
  /** User ID */
  id: number
  /** Username */
  username: string
  /** Current quota balance */
  quota: number
  /** Total used quota */
  used_quota: number
  /** Total request count */
  request_count: number
  /** Affiliate quota (pending rewards) */
  aff_quota: number
  aff_debt?: number
  /** Total affiliate quota earned (historical) */
  aff_history_quota: number
  /** Number of successful affiliate invites */
  aff_count: number
  /** User group */
  group: string
  trust_level_info?: import('@/stores/auth-store').TrustLevelInfo
  trust_level_tiers?: import('@/stores/auth-store').TrustLevelTier[]
}

/**
 * Topup record status
 */
export type TopupStatus = 'success' | 'pending' | 'failed' | 'expired'

/**
 * Topup billing record
 */
export interface TopupRecord {
  /** Record ID */
  id: number
  /** User ID */
  user_id: number
  /** Immutable raw credit snapshot when available. */
  credited_quota?: number
  /** Immutable gateway settlement currency. */
  settlement_currency?: string
  /** Deprecated integer projection of the legacy recharge batch amount. */
  amount: number
  /** Exact legacy recharge batch snapshot in millionths for fractional top-ups. */
  platform_amount_micros?: number
  /** Payment amount (actual fiat money paid) */
  money: number
  /** Fiat currency used by the selected payment gateway. */
  currency?: string
  /** Trade/order number */
  trade_no: string
  /** Payment method type */
  payment_method: string
  /** Creation timestamp */
  create_time: number
  /** Completion timestamp */
  complete_time?: number
  /** Payment status */
  status: TopupStatus
}

/** Server-side billing history sort fields. */
export type BillingHistorySortBy =
  | 'create_time'
  | 'amount'
  | 'money'
  | 'status'
  | 'payment_method'
  | 'user_id'
  | 'trade_no'

/** Server-side billing history sort direction. */
export type BillingHistorySortOrder = 'asc' | 'desc'

/**
 * Billing history response
 */
export interface BillingHistoryResponse {
  items: TopupRecord[]
  total: number
}

/**
 * Complete order request (admin only)
 */
export interface CompleteOrderRequest {
  trade_no: string
}
