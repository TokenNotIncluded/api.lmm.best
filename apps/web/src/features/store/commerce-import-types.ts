/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StoreVisibility } from './access-types'

export type CommerceImportAuthScope = {
  userId: number | undefined
  sessionId: string | undefined
}
export type CommerceImportConfig = {
  enabled: boolean
  redirect_uri: string
  trusted_origins: string[]
  environment_override?: boolean
}
export type CommerceImportConnection = {
  id: string
  issuer: string
  client_id: string
  shop_id?: string
  shop_name?: string
  scope: string
  status: 'pending' | 'active' | 'reauthorize' | 'disconnected'
  maximum_cards_per_request: number
}
export type CommerceImportText = string | Record<string, string>
export type CommerceImportVariant = {
  id: string
  name: CommerceImportText
  description?: CommerceImportText
  price?: string | null
  currency?: string
  enabled: boolean
  attributes?: Record<string, unknown>
  [key: string]: unknown
}
export type CommerceImportListing = {
  schema: 'extore.product-listing.v1'
  id: string
  shop_id: string
  revision: string
  redemption_url: string
  semantics: Record<string, string>
  product: {
    name: CommerceImportText
    description?: CommerceImportText
    public?: boolean
    mode?: string
    [key: string]: unknown
  }
  variants: CommerceImportVariant[]
  [key: string]: unknown
}
export type CommerceImportMapping = {
  external_product_id: string
  local_product_id: string
  revision: string
  variants: { external_id: string; local_variant_id: string }[]
}
export type CommerceImportCatalog = {
  schema: 'extore.commerce-catalog.v1'
  issuer: string
  shop: { id: string; name: string }
  grant_id: string
  products: CommerceImportListing[]
  mappings: CommerceImportMapping[]
}
export type CommerceImportDraft = {
  product_id: string
  revision: string
  visibility: StoreVisibility
  variants: { external_id: string; price_quota: number; enabled: boolean }[]
  confirmed: true
}
export type CommerceImportRestock = {
  product_id: string
  variant_id: string
  count: number
  expected_revision: string
  label: string
}
export type CommerceImportRequest = {
  id: string
  product_id: string
  variant_id: string
  count: number
  label?: string
  status: 'pending' | 'received' | 'imported' | 'manual_recovery'
  batch_id?: string
  error_code?: string
  issuance_uncertain?: boolean
  created_at?: number
  recovery_expires?: number
}
