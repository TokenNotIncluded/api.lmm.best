/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export function storeProductShareUrl(productId: string, origin: string) {
  if (!/^[A-Za-z0-9-]{1,64}$/.test(productId)) {
    throw new Error('Product not found')
  }
  const base = new URL(origin)
  if (base.protocol !== 'https:' && base.protocol !== 'http:') {
    throw new Error('Product not found')
  }
  return new URL(
    `/store/products/${encodeURIComponent(productId)}`,
    base.origin
  ).href
}

export async function shareStoreProduct(
  product: { id: string; title: string },
  origin: string,
  actions: {
    share?: (data: { title: string; url: string }) => Promise<void>
    copy: (url: string) => Promise<boolean>
  }
): Promise<'shared' | 'copied' | 'cancelled'> {
  const url = storeProductShareUrl(product.id, origin)
  if (actions.share) {
    try {
      await actions.share({ title: product.title, url })
      return 'shared'
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') {
        return 'cancelled'
      }
    }
  }
  if (!(await actions.copy(url))) throw new Error('Copy failed')
  return 'copied'
}
