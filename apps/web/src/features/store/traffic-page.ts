/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useCallback, useState } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { storeTrafficApi, type StoreTrafficKind } from './traffic-api'

type TrafficProduct = { id: string; seller_id: number }
type ObservedProduct = {
  product: TrafficProduct
  visible: boolean
  timer?: ReturnType<typeof setTimeout>
}

export type StoreTrafficPage = {
  readonly pageKey: string
  readonly startedAt: number
  observe: (element: HTMLElement, product: TrafficProduct) => () => void
  record: (product: TrafficProduct, kind: StoreTrafficKind) => Promise<void>
}

// The scope belongs to one mounted browse/detail page. Its successful receipts
// survive card remounts, filtering and layout changes, without persistent storage.
export function createStoreTrafficPage(): StoreTrafficPage {
  let pageKey = ''
  try {
    pageKey = crypto.randomUUID()
  } catch {
    // Optional analytics must not prevent browsing in older/insecure clients.
  }
  const startedAt = Math.floor(Date.now() / 1000)
  const sent = new Set<string>()
  const pending = new Set<string>()
  const observed = new Map<HTMLElement, ObservedProduct>()
  let observer: IntersectionObserver | undefined

  const owner = (product: TrafficProduct) =>
    useAuthStore.getState().auth.user?.id === product.seller_id

  async function record(product: TrafficProduct, kind: StoreTrafficKind) {
    const key = `${product.id}:${kind}`
    const auth = useAuthStore.getState().auth
    if (
      !pageKey ||
      auth.user?.id === product.seller_id ||
      sent.has(key) ||
      pending.has(key)
    ) {
      return
    }
    pending.add(key)
    try {
      await storeTrafficApi.record(
        product.id,
        {
          kind,
          page_key: pageKey,
          page_started_at: startedAt,
        },
        { userId: auth.user?.id, sessionId: auth.session?.sid }
      )
      sent.add(key)
    } catch {
      // Analytics is optional and must never interrupt browsing or checkout.
    } finally {
      pending.delete(key)
    }
  }

  function cancel(item: ObservedProduct) {
    if (item.timer !== undefined) clearTimeout(item.timer)
    item.timer = undefined
  }

  function schedule(element: HTMLElement, item: ObservedProduct) {
    if (
      item.timer !== undefined ||
      !item.visible ||
      document.visibilityState !== 'visible' ||
      owner(item.product) ||
      sent.has(`${item.product.id}:impression`) ||
      pending.has(`${item.product.id}:impression`)
    ) {
      return
    }
    item.timer = setTimeout(() => {
      item.timer = undefined
      if (
        observed.get(element) === item &&
        element.isConnected &&
        item.visible &&
        document.visibilityState === 'visible'
      ) {
        void record(item.product, 'impression')
      }
    }, 500)
  }

  function visibilityChanged() {
    for (const [element, item] of observed) {
      cancel(item)
      // A fresh observation on return avoids using background-tab geometry.
      item.visible = false
      if (document.visibilityState === 'visible') {
        observer?.unobserve(element)
        observer?.observe(element)
      }
    }
  }

  function observe(element: HTMLElement, product: TrafficProduct) {
    if (
      !pageKey ||
      typeof IntersectionObserver === 'undefined' ||
      owner(product)
    ) {
      return () => {}
    }
    if (!observer) {
      observer = new IntersectionObserver(
        (entries) => {
          for (const entry of entries) {
            const element = entry.target as HTMLElement
            const item = observed.get(element)
            if (!item) continue
            item.visible =
              entry.isIntersecting && entry.intersectionRatio >= 0.5
            if (item.visible) schedule(element, item)
            else cancel(item)
          }
        },
        { threshold: [0, 0.5] }
      )
      document.addEventListener('visibilitychange', visibilityChanged)
    }
    const item: ObservedProduct = { product, visible: false }
    observed.set(element, item)
    observer.observe(element)
    return () => {
      cancel(item)
      observed.delete(element)
      observer?.unobserve(element)
      if (!observed.size) {
        observer?.disconnect()
        observer = undefined
        document.removeEventListener('visibilitychange', visibilityChanged)
      }
    }
  }

  return { pageKey, startedAt, observe, record }
}

export function useStoreTrafficPage() {
  const [page] = useState(createStoreTrafficPage)
  return page
}

export function useStoreProductImpression(
  page: StoreTrafficPage | undefined,
  product: TrafficProduct
) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const productId = product.id
  const sellerId = product.seller_id
  return useCallback(
    (element: HTMLElement | null) => {
      if (!element || !page || userId === sellerId) return
      return page.observe(element, { id: productId, seller_id: sellerId })
    },
    [page, productId, sellerId, userId]
  )
}
