/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
const ORDER_KEY = 'lmm.store.guest-order-ids.v1'
export function storeGuestOrderIds(guestId: string): string[] {
  try {
    const map = JSON.parse(sessionStorage.getItem(ORDER_KEY) || '{}') as Record<
      string,
      unknown
    >
    return Array.isArray(map[guestId])
      ? map[guestId].filter(
          (id: unknown): id is string =>
            typeof id === 'string' && /^[a-zA-Z0-9-]{1,64}$/.test(id)
        )
      : []
  } catch {
    return []
  }
}
export function rememberStoreGuestOrder(guestId: string, id: string) {
  const ids = storeGuestOrderIds(guestId)
  if (!ids.includes(id)) {
    let map: Record<string, string[]> = {}
    try {
      map = JSON.parse(sessionStorage.getItem(ORDER_KEY) || '{}') as Record<
        string,
        string[]
      >
    } catch {
      /* use an empty ID-only map */
    }
    sessionStorage.setItem(
      ORDER_KEY,
      JSON.stringify({ ...map, [guestId]: [id, ...ids] })
    )
  }
}
