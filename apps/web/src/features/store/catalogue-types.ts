/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StoreConfig, StoreProduct } from './types'

export type StoreCatalogueSort = 'comprehensive' | 'sales' | 'newest'
export type StoreCatalogueStock = 'in_stock' | 'out_of_stock'
export type StoreCatalogueTag =
  | StoreCatalogueStock
  | 'guest_purchase'
  | 'auto_delivery'
  | 'ai_processing'

export interface StoreProductCatalogue {
  custom_tags: string[]
  auto_delivery: boolean
  ai_processing: boolean
}

export interface StoreCatalogueProduct extends StoreProduct {
  // Optional while a browser is connected to a server without floor 5.
  catalogue?: StoreProductCatalogue
  display_tags?: StoreCatalogueTag[]
  net_paid_quantity?: number | null
  visibility?: 'public' | 'registered' | 'private'
  purchase_login_required?: boolean
}

export interface StoreCatalogueConfig extends StoreConfig {
  store_catalogue_supported?: boolean
  store_collections_supported?: boolean
  store_likes_supported?: boolean
  store_access_supported?: boolean
}

export interface StoreCatalogueFilters {
  sort?: StoreCatalogueSort
  // A literal seller-defined tag. Derived tags use their separate filters.
  tag?: string
  stock?: StoreCatalogueStock
  autoDelivery?: boolean
  aiProcessing?: boolean
  guestPurchase?: boolean
}

export interface StoreCatalogueSearch extends StoreCatalogueFilters {
  search?: string
  page?: number
  sellerId?: number
}

export interface GuestStoreCartRef {
  product_id: string
  variant_id: string
  quantity: number
}

export interface StoreCartRow extends GuestStoreCartRef {
  id: string
  created_at: number
  updated_at: number
}

export type StoreCollectionUnavailableReason =
  | 'not_visible'
  | 'insufficient_stock'
  | 'purchase_limit'
  | 'unavailable'

export interface StoreCartItem extends StoreCartRow {
  valid: boolean
  unavailable_reason: StoreCollectionUnavailableReason | null
  product: StoreCatalogueProduct | null
}

export interface StoreFavoriteItem {
  product_id: string
  created_at: number
  valid: boolean
  unavailable_reason: StoreCollectionUnavailableReason | null
  product: StoreCatalogueProduct | null
}

export interface StoreCollectionPagination {
  offset?: number
  limit?: number
}

export interface StoreCollectionsCleanup {
  cart_deleted: number
  favorites_deleted: number
}

export interface StoreProductLikes {
  supported: boolean
  count: number | null
  liked: boolean
}
