/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
const key = 'lmm.store.social-intent.v1'
const maxAge = 10 * 60 * 1000
export type StoreSocialAction = 'favorite' | 'like'
type Intent = {
  productId: string
  action: StoreSocialAction
  createdAt: number
}

// This is an explicit post-login action, never a local favorite/like record.
export function rememberStoreSocialIntent(
  productId: string,
  action: StoreSocialAction
) {
  if (
    typeof window === 'undefined' ||
    !/^[A-Za-z0-9-]{1,64}$/.test(productId)
  ) {
    return
  }
  try {
    window.sessionStorage.setItem(
      key,
      JSON.stringify({ productId, action, createdAt: Date.now() })
    )
  } catch {
    /* Login remains available when browser storage is disabled. */
  }
}

export function consumeStoreSocialIntent(
  productId: string,
  action: StoreSocialAction
): boolean {
  if (typeof window === 'undefined') return false
  try {
    const raw = window.sessionStorage.getItem(key)
    if (!raw) return false
    const value = JSON.parse(raw) as Partial<Intent>
    const age = Date.now() - Number(value.createdAt)
    if (
      !Number.isFinite(age) ||
      age < 0 ||
      age > maxAge ||
      !['favorite', 'like'].includes(String(value.action))
    ) {
      window.sessionStorage.removeItem(key)
      return false
    }
    if (value.productId !== productId || value.action !== action) return false
    // Consume once before dispatch. PUT is idempotent; a failed request leaves
    // a real, visible retry button rather than silently repeating on rerenders.
    window.sessionStorage.removeItem(key)
    return true
  } catch {
    return false
  }
}

export function storeSocialSignInUrl(productId: string) {
  return `/sign-in?redirect=${encodeURIComponent(`/store/products/${encodeURIComponent(productId)}`)}`
}
