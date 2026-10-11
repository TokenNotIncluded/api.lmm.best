/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export type StoreVisibility = 'public' | 'registered' | 'private'
export interface StoreAccessSettings {
  visibility: StoreVisibility
  purchase_login_required: boolean
  pickup_login_required: boolean
}
export interface StoreSellerTerms {
  version: string
  content: string
  required: true
  configured: boolean
  accepted: boolean
  updated_at: number
}
export function storeAccessSupported(
  config?: { store_access_supported?: unknown } | null
) {
  return config?.store_access_supported === true
}
export function storeTermsSupported(
  config?: {
    store_terms_supported?: unknown
    store_access_supported?: unknown
  } | null
) {
  // A present capability is authoritative, including false or malformed data.
  // Only servers predating the separate terms flag inherit the access contract.
  return config && Object.hasOwn(config, 'store_terms_supported')
    ? config.store_terms_supported === true
    : storeAccessSupported(config)
}
export function storeVisibility(product: {
  visibility?: StoreVisibility
  test_mode?: boolean
}): StoreVisibility {
  return (
    product.visibility ?? (product.test_mode === true ? 'private' : 'public')
  )
}
export function storePurchaseLoginRequired(product: {
  visibility?: StoreVisibility
  purchase_login_required?: boolean
}) {
  return (
    product.visibility === 'registered' ||
    product.visibility === 'private' ||
    product.purchase_login_required !== false
  )
}
