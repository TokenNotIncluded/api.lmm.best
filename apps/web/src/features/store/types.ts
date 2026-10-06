/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
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
export interface StoreProduct {
  id: string
  seller_id: number
  title: string
  description: string
  image_urls: string[]
  contact: string
  links: StoreLink[]
  price_quota: number
  template: 'card-key' | 'text' | 'custom-text'
  delivery_strategy: 'sequential' | 'random'
  payment_methods: StorePaymentMethod[]
  pickup_login_required: boolean
  pickup_code_required: boolean
  email_pickup_link: boolean
  status: 'draft' | 'pending' | 'published' | 'rejected' | 'paused'
  official: boolean
  available_stock: number
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
  | 'promotion_expires_at'
  | 'created_at'
  | 'updated_at'
  | 'review_note'
  | 'status'
  | 'trading_paused'
>
export interface StoreOrder {
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
  payment_method: StorePaymentMethod
  status:
    | 'pending'
    | 'paid'
    | 'cancelled'
    | 'expired'
    | 'reconciliation_pending'
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
  payment_issue_code?: string
  verified_payment_issue_at?: number
}
export interface StoreConfig {
  fee_bps: number
  promotion_quota: number
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
  product_id: string
  quantity: number
  payment_method: StorePaymentMethod
  request_key: string
  disclaimer_version?: string
  pickup_code?: string
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
  order_id: string
  quantity: number
  product_title: string
  status: StoreOrder['status']
  pickup_login_required: boolean
  pickup_code_required: boolean
  pickup_login_satisfied: boolean
}
export interface StoreClaim {
  order_id: string
  product_title: string
  items: string[]
}
export interface StoreStock {
  id: string
  product_id: string
  state: 'available' | 'reserved' | 'delivered'
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
