/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StoreVisibility } from './access-types'
import type { StoreDeliveryTemplate } from './delivery-template'

export type StorePaymentMethod =
  | 'balance'
  | 'platform:waffo_pancake'
  | 'platform:linuxdo'
  | 'external:epay'
  | 'external:waffo_pancake'
export interface StoreLink {
  title: string
  url: string
  description: string
}
export interface StoreLinkPreset extends StoreLink {
  id: string
}
export interface StoreSeller {
  id: number
  username: string
  display_name: string
  avatar_url?: string
  contact_email?: string
}
export interface StoreMerchantHome {
  seller: StoreSeller
  biography: string
  announcement: string
  header_image: string
  version: number
}
export type StoreMerchantHomeInput = Omit<
  StoreMerchantHome,
  'seller' | 'version'
> & { expected_version: number }
export interface StoreCategory {
  id: string
  name: string
  sort_order: number
  active: boolean
  created_at: number
  updated_at: number
}
export interface StoreCategoryList {
  supported: boolean
  items: StoreCategory[]
  offset: number
  limit: number
  has_more: boolean
}
export type StoreCategoryInput = Pick<
  StoreCategory,
  'name' | 'sort_order' | 'active'
>
export interface StoreVariant {
  id: string
  product_id: string
  name: string
  price_quota: number
  template: StoreDeliveryTemplate
  enabled: boolean
  created_at: number
  updated_at: number
  is_default: boolean
  inventory_total: number
  inventory_available: number
  reserved_stock: number
  sale_available: number
  trading_paused: boolean
}
export type StoreVariantInput = Pick<
  StoreVariant,
  'name' | 'price_quota' | 'template' | 'enabled'
>
export interface StoreProduct {
  category_id?: string
  category?: Pick<StoreCategory, 'id' | 'name'> & { active?: boolean }
  id: string
  seller_id: number
  seller?: StoreSeller
  default_variant_id?: string
  variants?: StoreVariant[]
  inventory_total?: number
  inventory_available?: number
  price_min_quota?: number
  price_max_quota?: number
  test_mode?: boolean
  visibility?: StoreVisibility
  purchase_login_required?: boolean
  title: string
  description: string
  /** Logo at 0, header at 1, then gallery. Optional role slots may be empty.
   * Values are HTTP(S) images or validated static SVG data URIs. */
  image_urls: string[]
  contact: string
  links: StoreLink[]
  price_quota: number
  template: StoreDeliveryTemplate
  delivery_strategy: 'sequential' | 'random'
  payment_methods: StorePaymentMethod[]
  pickup_login_required: boolean
  pickup_code_required: boolean
  email_pickup_link: boolean
  status:
    | 'draft'
    | 'pending'
    | 'published'
    | 'rejected'
    | 'paused'
    | 'off_shelf'
    | 'unlisted'
  official: boolean
  available_stock: number
  sale_limit?: number | null
  max_quantity_per_order?: number | null
  max_quantity_per_buyer?: number | null
  buyer_purchase_remaining?: number | null
  paid_quantity?: number
  reserved_quantity?: number
  sale_available?: number
  promotion_expires_at: number
  created_at: number
  updated_at: number
  review_note: string
  trading_paused?: boolean
}
export type StoreProductInput = Omit<
  StoreProduct,
  | 'id'
  | 'seller_id'
  | 'official'
  | 'available_stock'
  | 'default_variant_id'
  | 'variants'
  | 'inventory_total'
  | 'inventory_available'
  | 'price_min_quota'
  | 'price_max_quota'
  | 'sale_limit'
  | 'buyer_purchase_remaining'
  | 'paid_quantity'
  | 'reserved_quantity'
  | 'sale_available'
  | 'promotion_expires_at'
  | 'created_at'
  | 'updated_at'
  | 'review_note'
  | 'status'
  | 'trading_paused'
  | 'category'
>
export interface StoreOrder {
  variant_id?: string
  variant_name?: string
  delivery_template?: string
  id: string
  trade_no: string
  buyer_id: number
  seller_id: number
  product_id: string
  product_title: string
  quantity: number
  unit_price_quota: number
  price_quota: number
  fee_quota: number
  payment_method: StorePaymentMethod | 'free'
  promotion_code?: string
  status:
    | 'pending'
    | 'paid'
    | 'cancelled'
    | 'expired'
    | 'reconciliation_pending'
    | 'refund_pending'
    | 'refunded'
  amount_minor: number
  currency: string
  frozen_usd_fx: string | number
  created_at: number
  paid_at: number
  expires_at: number
  pickup_login_required: boolean
  pickup_code_required: boolean
  email_pickup_link: boolean
  payment_issued?: boolean
  email_delivery_status?: string
  payment_issue_code?: string
  verified_payment_issue_at?: number
}
export interface StoreConfig {
  fee_bps: number
  promotion_quota: number
  product_test_mode_supported?: boolean
  store_access_supported?: boolean
  store_catalogue_supported?: boolean
  store_categories_supported?: boolean
  store_svg_media_supported?: boolean
  store_merchant_home_supported?: boolean
  product_purchase_limits_supported?: boolean
  product_link_presets?: StoreLinkPreset[]
  minimum_unit_price_quota?: number
  recipient_id?: number
  linuxdo_units_per_usd?: string
  disclaimer_version: string
  disclaimer_text: string
  platform_payment_methods: {
    provider: StorePaymentMethod
    configured: boolean
    unavailable_code?: string
  }[]
  platform_payment_catalog?: {
    provider?: StorePaymentMethod
    payment_type: string
    name: string
    supported: boolean
    configured: boolean
    unavailable_code?: string
  }[]
}
export interface StoreGateway {
  provider: StorePaymentMethod
  enabled: boolean
  configured: boolean
  category?: 'platform' | 'external'
  category_enabled?: boolean
  effective_enabled?: boolean
  currency?: string
  gateway_url?: string
  partner_id?: string
  payment_type?: string
  merchant_id?: string
  store_id?: string
  product_id?: string
  environment?: string
  has_key: boolean
  has_private_key: boolean
  callback_urls?: Record<string, string>
  unavailable_code?: string
}
export type StoreGatewayInput = Omit<
  StoreGateway,
  | 'configured'
  | 'category'
  | 'category_enabled'
  | 'effective_enabled'
  | 'has_key'
  | 'has_private_key'
  | 'callback_urls'
  | 'unavailable_code'
> & { key?: string; private_key?: string }
export interface StorePaymentSettings {
  categories: StorePaymentCategories
  items: StoreGateway[]
  external_eligible: boolean
  balance_quota: number
  fee_bps: number
}
export interface StorePaymentCategories {
  platform_enabled: boolean
  external_enabled: boolean
}
export interface StoreDisclaimer {
  version: string
  text: string
  accepted: boolean
}
export interface StoreCheckoutInput {
  variant_id?: string
  product_id: string
  quantity: number
  payment_method: StorePaymentMethod | 'free'
  promotion_code?: string
  request_key: string
  disclaimer_version?: string
  seller_terms_version?: string
  accept_seller_terms?: boolean
  pickup_code?: string
  pickup_email?: string
}
export interface StorePaymentSession {
  order_id: string
  payment_url?: string
  method: 'GET' | 'POST' | 'balance'
  parameters?: Record<string, string>
  amount_minor: number
  amount: string
  currency: string
  status: string
}
export interface StoreCheckoutResult {
  order: StoreOrder
  created: boolean
}
export interface StoreClaimMetadata {
  variant_id?: string
  variant_name?: string
  delivery_template?: string
  order_id: string
  quantity: number
  product_title: string
  status: StoreOrder['status']
  pickup_login_required: boolean
  pickup_code_required: boolean
  pickup_login_satisfied: boolean
}
export interface StoreClaim {
  variant_id?: string
  variant_name?: string
  delivery_template?: string
  order_id: string
  trade_no?: string
  quantity?: number
  product_id?: string
  product_title: string
  product_description?: string
  product_links?: StoreLink[]
  items: string[]
  item_stock_ids?: string[]
  item_positions?: number[]
}
export interface StoreStock {
  variant_id?: string | null
  id: string
  product_id: string
  state: 'available' | 'reserved' | 'delivered' | 'refunded'
  created_at: number
}
export interface StorePromotion {
  id: string
  product_id: string
  seller_id: number
  months: number
  quota: number
  expires_at: number
  created_at: number
}
export interface StorePage<T> {
  items: T[]
  offset: number
  limit: number
  has_more: boolean
}
export interface StoreOrderSummary {
  id: string
  trade_no: string
  product_title: string
  quantity: number
  status: StoreOrder['status']
  created_at: number
  paid_at: number
  pickup_login_required: boolean
  pickup_code_required: boolean
  pickup_url?: string
}
