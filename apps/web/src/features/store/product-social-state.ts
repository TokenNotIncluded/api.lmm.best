/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import type { StoreProductLikes } from './catalogue-types'
import { currentStoreViewer, type StoreViewer } from './store-viewer'

type ProductProjection = { likes?: StoreProductLikes | null }
type Projection = {
  viewer: StoreViewer
  data: StoreProductLikes
  requestedAt: number
}
const projections = new WeakMap<ProductProjection, Projection>()

// The server owns these values. Bind them to the account that fetched the
// product, rather than borrowing an anonymous/previous-account projection.
export async function storeSocialProductResponse<T>(
  request: () => Promise<T>,
  products: (data: T) => (ProductProjection | null | undefined)[]
): Promise<T> {
  const viewer = currentStoreViewer()
  const requestedAt = Date.now()
  const data = await request()
  if (currentStoreViewer() !== viewer) {
    throw new Error(
      'Your account changed. Refresh this page before continuing.'
    )
  }
  for (const product of products(data)) {
    if (product?.likes) {
      projections.set(product, { viewer, data: product.likes, requestedAt })
    }
  }
  return data
}

export function storeProductSocialProjection(
  product: ProductProjection,
  viewer: string
) {
  const projection = projections.get(product)
  return projection?.viewer === viewer ? projection : undefined
}
