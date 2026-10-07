/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import type {
  GuestStoreCartRef,
  StoreCartItem,
  StoreCartRow,
  StoreCatalogueConfig,
  StoreCatalogueProduct,
  StoreCatalogueSearch,
  StoreCollectionPagination,
  StoreCollectionsCleanup,
  StoreFavoriteItem,
  StoreProductCatalogue,
  StoreProductLikes,
} from './catalogue-types'
import { storeSocialProductResponse } from './product-social-state'
import { STORE_PURCHASE_LIMIT_COPY } from './purchase-limits-copy'
import type { StorePage, StoreSeller } from './types'

type Envelope<T> = {
  success: boolean
  code?: string
  message?: string
  data: T
}

const root = '/api/store'
const options = { skipErrorHandler: true, skipBusinessError: true }

function storeRequestError(body?: { code?: unknown; message?: unknown }) {
  if (body?.code === 'STORE_VARIANT_REQUIRED') {
    return 'Choose a variant before ordering.'
  }
  if (body?.code === 'STORE_UPGRADE_IN_PROGRESS') {
    return 'Shop upgrade is in progress. Existing orders are still accessible.'
  }
  if (body?.code === 'STORE_PURCHASE_LIMIT') {
    return STORE_PURCHASE_LIMIT_COPY.error
  }
  return typeof body?.message === 'string' && body.message
    ? body.message
    : 'Store request failed'
}

async function unwrap<T>(request: Promise<{ data: Envelope<T> }>): Promise<T> {
  let response: { data: Envelope<T> }
  try {
    response = await request
  } catch (error) {
    const failure = error as {
      name?: string
      code?: string
      response?: { data?: { code?: unknown; message?: unknown } }
    }
    if (
      failure?.name === 'AbortError' ||
      failure?.name === 'CanceledError' ||
      failure?.code === 'ERR_CANCELED'
    ) {
      throw error
    }
    throw new Error(storeRequestError(failure?.response?.data))
  }
  if (response.data.success !== true) {
    throw new Error(storeRequestError(response.data))
  }
  return response.data.data
}

function positiveInteger(value: number | undefined, fallback: number) {
  return Number.isSafeInteger(value) && (value as number) > 0
    ? (value as number)
    : fallback
}

function pageParams(pagination: StoreCollectionPagination = {}) {
  const offset = pagination.offset
  return {
    offset:
      Number.isSafeInteger(offset) && (offset as number) >= 0 ? offset : 0,
    limit: positiveInteger(pagination.limit, 100),
  }
}

async function allPages<T>(
  getPage: (
    pagination: StoreCollectionPagination,
    signal?: AbortSignal
  ) => Promise<StorePage<T>>,
  identity: (item: T) => string,
  signal?: AbortSignal
): Promise<T[]> {
  const items = new Map<string, T>()
  let offset = 0
  for (;;) {
    const page = await getPage({ offset, limit: 100 }, signal)
    for (const item of page.items) items.set(identity(item), item)
    if (!page.has_more) return Array.from(items.values())
    const next = page.offset + page.limit
    // A malformed response must not leave a cart spinning through one page.
    if (!Number.isSafeInteger(next) || next <= offset || !page.items.length) {
      throw new Error('Store request failed')
    }
    offset = next
  }
}

function cart(
  pagination: StoreCollectionPagination = {},
  signal?: AbortSignal
) {
  return storeSocialProductResponse(
    () =>
      unwrap<StorePage<StoreCartItem>>(
        api.get(`${root}/cart`, {
          ...options,
          params: pageParams(pagination),
          signal,
        })
      ),
    (data) => data.items.map((item) => item.product)
  )
}

function favorites(
  pagination: StoreCollectionPagination = {},
  signal?: AbortSignal
) {
  return storeSocialProductResponse(
    () =>
      unwrap<StorePage<StoreFavoriteItem>>(
        api.get(`${root}/favorites`, {
          ...options,
          params: pageParams(pagination),
          signal,
        })
      ),
    (data) => data.items.map((item) => item.product)
  )
}

export const catalogueApi = {
  config: (signal?: AbortSignal) =>
    unwrap<StoreCatalogueConfig>(
      api.get(`${root}/config`, { ...options, signal })
    ),
  products: (search: StoreCatalogueSearch = {}, signal?: AbortSignal) =>
    storeSocialProductResponse(
      () =>
        unwrap<
          StorePage<StoreCatalogueProduct> & { seller?: StoreSeller | null }
        >(
          api.get(`${root}/products`, {
            ...options,
            signal,
            params: {
              q: search.search ?? '',
              offset: (positiveInteger(search.page, 1) - 1) * 24,
              limit: 24,
              sort: search.sort ?? 'comprehensive',
              ...(search.sellerId ? { seller_id: search.sellerId } : {}),
              ...(search.tag ? { tag: search.tag } : {}),
              ...(search.stock ? { stock: search.stock } : {}),
              ...(search.autoDelivery === undefined
                ? {}
                : { auto_delivery: search.autoDelivery }),
              ...(search.aiProcessing === undefined
                ? {}
                : { ai_processing: search.aiProcessing }),
              ...(search.guestPurchase === undefined
                ? {}
                : { guest_purchase: search.guestPurchase }),
            },
          })
        ),
      (data) => data.items
    ),
  product: (id: string, signal?: AbortSignal) =>
    storeSocialProductResponse(
      () =>
        unwrap<StoreCatalogueProduct>(
          api.get(`${root}/products/${encodeURIComponent(id)}`, {
            ...options,
            signal,
          })
        ),
      (data) => [data]
    ),
  saveCatalogue: (id: string, body: StoreProductCatalogue) =>
    unwrap<StoreProductCatalogue>(
      api.put(
        `${root}/products/${encodeURIComponent(id)}/catalogue`,
        body,
        options
      )
    ),
  cart,
  allCart: (signal?: AbortSignal) => allPages(cart, (item) => item.id, signal),
  // PUT writes an absolute quantity and returns a stored reference. Fetch the
  // collection again before rendering availability, prices or checkout links.
  putCart: (body: GuestStoreCartRef) =>
    unwrap<StoreCartRow>(api.put(`${root}/cart`, body, options)),
  removeCart: (id: string) =>
    unwrap<null>(api.delete(`${root}/cart/${encodeURIComponent(id)}`, options)),
  clearCart: () => unwrap<null>(api.delete(`${root}/cart`, options)),
  favorites,
  allFavorites: (signal?: AbortSignal) =>
    allPages(favorites, (item) => item.product_id, signal),
  addFavorite: (productId: string) =>
    unwrap<null>(
      api.put(`${root}/favorites`, { product_id: productId }, options)
    ),
  removeFavorite: (productId: string) =>
    unwrap<null>(
      api.delete(`${root}/favorites/${encodeURIComponent(productId)}`, options)
    ),
  likes: (productId: string, signal?: AbortSignal) =>
    unwrap<StoreProductLikes>(
      api.get(`${root}/products/${encodeURIComponent(productId)}/likes`, {
        ...options,
        signal,
      })
    ),
  like: (productId: string) =>
    unwrap<StoreProductLikes>(
      api.put(
        `${root}/products/${encodeURIComponent(productId)}/likes`,
        {},
        options
      )
    ),
  unlike: (productId: string) =>
    unwrap<StoreProductLikes>(
      api.delete(
        `${root}/products/${encodeURIComponent(productId)}/likes`,
        options
      )
    ),
  clearFavorites: () => unwrap<null>(api.delete(`${root}/favorites`, options)),
  cleanup: () =>
    unwrap<StoreCollectionsCleanup>(
      api.post(`${root}/collections/cleanup`, {}, options)
    ),
}
