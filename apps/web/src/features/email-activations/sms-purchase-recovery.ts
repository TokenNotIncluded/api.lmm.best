/*
Copyright (C) 2026 LIghtJUNction
*/
import { useCallback, useSyncExternalStore } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import type { HeroSmsBatchPurchaseResult } from './sms-purchase'
import { HERO_SMS_MAX_QUANTITY } from './sms-selection'

export interface SmsPurchaseRecovery {
  userId: number
  offerId: string
  idempotencyKey: string
  requested: number
  item: number
}

const prefix = 'lmm-hero-sms-purchase-recovery:v1:'
const listeners = new Set<() => void>()
const snapshots = new Map<
  number,
  { raw: string | null; value: SmsPurchaseRecovery | null }
>()

function isRecovery(
  value: unknown,
  userId: number
): value is SmsPurchaseRecovery {
  if (!value || typeof value !== 'object') return false
  const record = value as SmsPurchaseRecovery
  return (
    record.userId === userId &&
    typeof record.offerId === 'string' &&
    record.offerId.length > 0 &&
    typeof record.idempotencyKey === 'string' &&
    record.idempotencyKey.length > 0 &&
    record.idempotencyKey.length <= 128 &&
    Number.isInteger(record.requested) &&
    record.requested >= 1 &&
    record.requested <= HERO_SMS_MAX_QUANTITY &&
    Number.isInteger(record.item) &&
    record.item >= 1 &&
    record.item <= record.requested
  )
}

export function readSmsPurchaseRecovery(
  userId: number | undefined,
  strict = false
) {
  if (userId === undefined) return null
  let raw: string | null
  try {
    raw = window.localStorage.getItem(`${prefix}${userId}`)
  } catch {
    if (strict) throw new Error('HeroSMS request failed')
    // Keep a previously loaded record if storage becomes temporarily unavailable.
    return snapshots.get(userId)?.value ?? null
  }
  const cached = snapshots.get(userId)
  if (cached?.raw === raw) return cached.value
  let value: SmsPurchaseRecovery | null = null
  try {
    const parsed: unknown = raw ? JSON.parse(raw) : null
    if (isRecovery(parsed, userId)) value = parsed
  } catch {
    // A malformed browser record cannot be used as a purchase request.
  }
  snapshots.set(userId, { raw, value })
  return value
}

function samePurchase(left: SmsPurchaseRecovery, right: SmsPurchaseRecovery) {
  return (
    left.userId === right.userId &&
    left.offerId === right.offerId &&
    left.idempotencyKey === right.idempotencyKey
  )
}

function notify() {
  for (const listener of listeners) listener()
}

export function saveSmsPurchaseRecovery(record: SmsPurchaseRecovery) {
  const existing = readSmsPurchaseRecovery(record.userId, true)
  if (existing && !samePurchase(existing, record)) {
    throw new Error('HeroSMS request failed')
  }
  // This must succeed before sending a purchase. Memory alone cannot survive
  // a page reload between the provider purchase and its HTTP response.
  window.localStorage.setItem(
    `${prefix}${record.userId}`,
    JSON.stringify(record)
  )
  snapshots.delete(record.userId)
  notify()
}

export function clearSmsPurchaseRecovery(record: SmsPurchaseRecovery) {
  try {
    const existing = readSmsPurchaseRecovery(record.userId, true)
    if (!existing || !samePurchase(existing, record)) return
    window.localStorage.removeItem(`${prefix}${record.userId}`)
  } catch {
    // Keep recovery blocked if the durable record cannot be removed.
    return
  }
  snapshots.delete(record.userId)
  notify()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  window.addEventListener('storage', listener)
  return () => {
    listeners.delete(listener)
    window.removeEventListener('storage', listener)
  }
}

export function useSmsPurchaseRecovery() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const getSnapshot = useCallback(
    () => readSmsPurchaseRecovery(userId),
    [userId]
  )
  const pending = useSyncExternalStore(subscribe, getSnapshot, () => null)
  return { userId, pending }
}

export function recoveryBatchResult(
  pending: SmsPurchaseRecovery
): HeroSmsBatchPurchaseResult {
  return {
    requested: pending.requested,
    orders: [],
    completedCount: pending.item - 1,
    failure: {
      code: 'REQUEST_FAILED',
      item: pending.item,
      ambiguous: true,
      offerId: pending.offerId,
      idempotencyKey: pending.idempotencyKey,
    },
  }
}
