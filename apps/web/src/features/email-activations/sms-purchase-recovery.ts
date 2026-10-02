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

async function withRecoveryLock<T>(
  record: SmsPurchaseRecovery,
  canProceed: () => boolean,
  mutate: () => T
) {
  const locks = window.navigator.locks
  if (typeof locks?.request !== 'function') {
    throw new Error('HeroSMS request failed')
  }
  return locks.request(
    `${prefix}${record.userId}`,
    { mode: 'exclusive' },
    () => {
      if (!canProceed()) throw new Error('HeroSMS request failed')
      // Keep every shared read/check/write synchronous inside this origin-wide
      // account lock. HTTP requests must only start after the lock is released.
      return mutate()
    }
  )
}

export async function saveSmsPurchaseRecovery(
  record: SmsPurchaseRecovery,
  canProceed: () => boolean = () => true
) {
  await withRecoveryLock(record, canProceed, () => {
    const existing = readSmsPurchaseRecovery(record.userId, true)
    if (existing && !samePurchase(existing, record)) {
      throw new Error('HeroSMS request failed')
    }
    // Persistence must succeed before POST so reloads cannot lose its identity.
    window.localStorage.setItem(
      `${prefix}${record.userId}`,
      JSON.stringify(record)
    )
    snapshots.delete(record.userId)
    notify()
  })
}

export async function clearSmsPurchaseRecovery(
  record: SmsPurchaseRecovery,
  canProceed: () => boolean = () => true,
  onSettled: () => void = () => {}
) {
  try {
    return await withRecoveryLock(record, canProceed, () => {
      const existing = readSmsPurchaseRecovery(record.userId, true)
      if (!existing || !samePurchase(existing, record)) return false
      window.localStorage.removeItem(`${prefix}${record.userId}`)
      snapshots.delete(record.userId)
      // Apply the matching settlement before releasing the lock. A later tab
      // can otherwise settle a newer attempt before this promise continues.
      onSettled()
      notify()
      return true
    })
  } catch {
    // No lock, failed storage, or an obsolete owner must leave recovery intact.
    return false
  }
}

export async function checkSmsPurchaseRecovery(
  record: SmsPurchaseRecovery,
  canProceed: () => boolean
) {
  try {
    return await withRecoveryLock(record, canProceed, () => {
      const existing = readSmsPurchaseRecovery(record.userId, true)
      return existing !== null && samePurchase(existing, record)
    })
  } catch {
    return false
  }
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
