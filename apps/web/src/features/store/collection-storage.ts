/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useSyncExternalStore } from 'react'

import type { GuestStoreCartRef } from './catalogue-types'

export type StoreCatalogueView = 'cards' | 'list'
export const GUEST_STORE_CART_KEY = 'lmm.store.guest-cart.v1'
export const STORE_CATALOGUE_VIEW_KEY = 'lmm.store.catalogue-view.v1'

const identifier = /^[a-zA-Z0-9-]{1,64}$/
const empty: GuestStoreCartRef[] = []
const listeners = new Set<() => void>()
let snapshot: GuestStoreCartRef[] = empty
let serialized: string | null | undefined
let storageUnavailable = false

function referenceKey(reference: GuestStoreCartRef) {
  return `${reference.product_id}:${reference.variant_id}`
}

export function sanitizeGuestStoreCart(value: unknown): GuestStoreCartRef[] {
  if (!Array.isArray(value)) return []
  const references = new Map<string, GuestStoreCartRef>()
  for (const item of value) {
    if (typeof item !== 'object' || item === null || Array.isArray(item)) {
      continue
    }
    const reference = item as Partial<GuestStoreCartRef>
    if (
      typeof reference.product_id !== 'string' ||
      !identifier.test(reference.product_id) ||
      typeof reference.variant_id !== 'string' ||
      !identifier.test(reference.variant_id) ||
      !Number.isSafeInteger(reference.quantity) ||
      (reference.quantity as number) <= 0
    ) {
      continue
    }
    // Never carry account, price, product detail or guest proof fields from a
    // storage payload into the anonymous cart snapshot or its next write.
    const safe: GuestStoreCartRef = {
      product_id: reference.product_id,
      variant_id: reference.variant_id,
      quantity: reference.quantity as number,
    }
    references.set(referenceKey(safe), safe)
  }
  return Array.from(references.values())
}

export function readGuestStoreCart(): GuestStoreCartRef[] {
  if (typeof window === 'undefined') return empty
  if (storageUnavailable) return snapshot
  try {
    const value = window.localStorage.getItem(GUEST_STORE_CART_KEY)
    if (serialized !== value) {
      serialized = value
      let decoded: unknown
      try {
        decoded = value ? JSON.parse(value) : []
      } catch {
        decoded = []
      }
      snapshot = sanitizeGuestStoreCart(decoded)
    }
  } catch {
    storageUnavailable = true
  }
  return snapshot
}

function notify() {
  for (const listener of listeners) listener()
}

function storeReferences(references: GuestStoreCartRef[]) {
  if (typeof window === 'undefined') return
  snapshot = sanitizeGuestStoreCart(references)
  serialized = snapshot.length ? JSON.stringify(snapshot) : null
  try {
    if (serialized === null) {
      window.localStorage.removeItem(GUEST_STORE_CART_KEY)
    } else {
      window.localStorage.setItem(GUEST_STORE_CART_KEY, serialized)
    }
  } catch {
    // A browser that blocks storage can still keep this anonymous cart for
    // the current page session. Account collections never enter this store.
    storageUnavailable = true
  }
  notify()
}

export function guestCartUpsert(reference: GuestStoreCartRef) {
  const safe = sanitizeGuestStoreCart([reference])[0]
  if (!safe) return false
  const key = referenceKey(safe)
  storeReferences([
    ...readGuestStoreCart().filter((item) => referenceKey(item) !== key),
    safe,
  ])
  return true
}

export function guestCartRemove(productId: string, variantId: string) {
  storeReferences(
    readGuestStoreCart().filter(
      (item) => item.product_id !== productId || item.variant_id !== variantId
    )
  )
}

export function guestCartClear() {
  storeReferences([])
}

function onStorage(event: StorageEvent) {
  if (event.key !== null && event.key !== GUEST_STORE_CART_KEY) return
  serialized = undefined
  storageUnavailable = false
  readGuestStoreCart()
  notify()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  if (listeners.size === 1 && typeof window !== 'undefined') {
    window.addEventListener('storage', onStorage)
  }
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0 && typeof window !== 'undefined') {
      window.removeEventListener('storage', onStorage)
    }
  }
}

export function useGuestStoreCart(): GuestStoreCartRef[] {
  return useSyncExternalStore(subscribe, readGuestStoreCart, () => empty)
}

export function readStoreCatalogueView(): StoreCatalogueView {
  if (typeof window === 'undefined') return 'cards'
  try {
    return window.localStorage.getItem(STORE_CATALOGUE_VIEW_KEY) === 'list'
      ? 'list'
      : 'cards'
  } catch {
    return 'cards'
  }
}

export function writeStoreCatalogueView(view: StoreCatalogueView) {
  if (view !== 'cards' && view !== 'list') return
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(STORE_CATALOGUE_VIEW_KEY, view)
  } catch {
    // The selected view can remain in component state without persistence.
  }
}
