/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export type StoreAnalyticsDays = 7 | 30 | 90 | 'all'
export type StoreAnalyticsScope = 'mine' | 'all'

export interface StoreAnalyticsCounts {
  impressions: number | null
  clicks: number | null
  orders: number
  paid_orders: number
  refunded_orders: number
  quantity_refunded_orders?: number
  amount_refunded_orders?: number
  ordered_quantity: number
  paid_quantity: number
  refunded_quantity: number
  net_paid_quantity: number
}

export interface StoreProductAnalyticsRow extends StoreAnalyticsCounts {
  product_id: string
  seller_id: number
  title: string
  status: string
}

export interface StoreAnalyticsPage {
  items: StoreProductAnalyticsRow[]
  offset: number
  limit: number
  has_more: boolean
  totals: StoreAnalyticsCounts
  traffic_supported: boolean
  traffic_retention_days: number
  traffic_since: number | null
}

export interface StoreAnalyticsConfig {
  retention_days: number
  dedupe_days: number
}

export interface StoreAnalyticsAuthScope {
  userId: number | undefined
  sessionId: string | undefined
}
