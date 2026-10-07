/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useLayoutEffect } from 'react'

import { useAuthStore, type AuthUser } from '@/stores/auth-store'

export type StoreViewer =
  | 'anonymous'
  | `account:${number}`
  | `account:${number}:unverified`
  | `account:${number}:disabled`

export function storeViewerKey(
  user: Pick<AuthUser, 'id' | 'status'> | null | undefined
): StoreViewer {
  if (!user || !Number.isSafeInteger(user.id) || user.id <= 0) {
    return 'anonymous'
  }
  if (user.status === undefined) return `account:${user.id}:unverified`
  if (user.status !== 1) return `account:${user.id}:disabled`
  return `account:${user.id}`
}

export const storeViewerId = storeViewerKey

export function currentStoreViewer(): StoreViewer {
  return storeViewerKey(useAuthStore.getState().auth.user)
}

const viewerNamespaces = new Set([
  'products',
  'product',
  'product-preview',
  'catalogue-products',
  'catalogue-product',
  'cart',
  'catalogue-cart',
  'favorites',
  'catalogue-favorites',
  'guest-cart-products',
  'guest-cart-product',
])

export function clearPreviousStoreViewer(
  client: QueryClient,
  viewer: StoreViewer
) {
  const filters = {
    predicate: (query: { queryKey: readonly unknown[] }) => {
      const key = query.queryKey
      if (key[0] !== 'store' || !viewerNamespaces.has(String(key[1]))) {
        return false
      }
      // Owned keys put viewer at index 2. Legacy product/list keys have no
      // verified partition and are removed when this guard becomes active.
      return key[2] !== viewer
    },
  }
  // Cancellation also aborts Axios requests when queryFn passes its signal.
  void client.cancelQueries(filters)
  client.removeQueries(filters)
}

type ViewerBinding = { count: number; unsubscribe: () => void }
const bindings = new WeakMap<QueryClient, ViewerBinding>()

function bindStoreViewer(client: QueryClient) {
  const bound = bindings.get(client)
  if (bound) {
    bound.count += 1
    return () => releaseStoreViewer(client)
  }
  let current = storeViewerKey(useAuthStore.getState().auth.user)
  clearPreviousStoreViewer(client, current)
  const unsubscribe = useAuthStore.subscribe((state) => {
    const next = storeViewerKey(state.auth.user)
    if (next === current) return
    current = next
    // Run directly on the auth transition so stale private details cannot
    // remain available until a component's subsequent effect is scheduled.
    clearPreviousStoreViewer(client, next)
  })
  bindings.set(client, { count: 1, unsubscribe })
  return () => releaseStoreViewer(client)
}

function releaseStoreViewer(client: QueryClient) {
  const bound = bindings.get(client)
  if (!bound) return
  bound.count -= 1
  if (bound.count > 0) return
  bound.unsubscribe()
  bindings.delete(client)
}

export function useStoreViewer(): StoreViewer {
  const user = useAuthStore((state) => state.auth.user)
  const client = useQueryClient()
  useLayoutEffect(() => bindStoreViewer(client), [client])
  // The identity changes during render. Callers must include it in query keys
  // and must not use previous-actor placeholder data while the new key loads.
  return storeViewerKey(user)
}
