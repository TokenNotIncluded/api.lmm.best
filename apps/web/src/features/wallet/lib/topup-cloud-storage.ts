/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { PendingTopupCloud } from './topup-cloud-success'

export const TOPUP_CLOUD_EVENT = 'wallet-topup-cloud-change'
const RETENTION_MS = 24 * 60 * 60 * 1000
const memory = new Map<number, PendingTopupCloud[]>()
const memoryOnly = new Set<number>()
let prepared: PendingTopupCloud | null = null

export const topupCloudStorageKey = (userId: number) =>
  `wallet-topup-cloud:${userId}`

export function readPendingTopups(userId: number): PendingTopupCloud[] {
  let values: unknown = memory.get(userId) ?? []
  try {
    if (!memoryOnly.has(userId)) {
      const raw = window.localStorage.getItem(topupCloudStorageKey(userId))
      values = raw ? JSON.parse(raw) : []
    }
  } catch {
    // Storage may be disabled; keep the current tab functional.
  }
  if (!Array.isArray(values)) return []
  return values
    .filter(
      (value): value is PendingTopupCloud =>
        value &&
        value.userId === userId &&
        typeof value.attemptId === 'string' &&
        value.attemptId.length > 0 &&
        Number.isFinite(value.launchedAt) &&
        Number.isFinite(value.expiresAt) &&
        value.expiresAt > Date.now() &&
        Number.isFinite(value.beforeQuota) &&
        Number.isFinite(value.expectedCredit) &&
        (value.tradeNo === undefined ||
          (typeof value.tradeNo === 'string' && value.tradeNo.length > 0))
    )
    .slice(-10)
}

function save(userId: number, entries: PendingTopupCloud[]) {
  memory.set(userId, entries)
  try {
    if (entries.length) {
      window.localStorage.setItem(
        topupCloudStorageKey(userId),
        JSON.stringify(entries)
      )
    } else window.localStorage.removeItem(topupCloudStorageKey(userId))
    memoryOnly.delete(userId)
  } catch {
    memoryOnly.add(userId)
    // These are UI receipts, not authentication or billing state.
  }
  window.dispatchEvent(new window.Event(TOPUP_CLOUD_EVENT))
}

export function prepareTopup(
  userId: number,
  beforeQuota: number,
  expectedCredit: number
) {
  const launchedAt = Date.now()
  const intent: PendingTopupCloud = {
    userId,
    beforeQuota,
    expectedCredit,
    launchedAt,
    expiresAt: launchedAt + RETENTION_MS,
    attemptId:
      globalThis.crypto?.randomUUID?.() ??
      `${launchedAt.toString(36)}-${Math.random().toString(36).slice(2)}`,
  }
  prepared = intent
  save(userId, [...readPendingTopups(userId), intent].slice(-10))
  return intent
}

/** Capture before awaiting the checkout request; another tab cannot replace it. */
export function capturePreparedTopup(userId: number | undefined) {
  return prepared?.userId === userId ? prepared : null
}

export function bindTopupOrder(
  intent: PendingTopupCloud | null,
  response: unknown
) {
  if (!intent || !response || typeof response !== 'object') return
  const result = response as {
    success?: boolean
    message?: string
    trade_no?: unknown
    data?: Record<string, unknown>
  }
  if (
    result.success === false ||
    !(result.success === true || result.message === 'success')
  ) {
    return
  }
  const tradeNo =
    result.trade_no ??
    result.data?.trade_no ??
    result.data?.out_trade_no ??
    result.data?.order_id
  if (typeof tradeNo !== 'string' || !tradeNo.trim()) return
  const entries = readPendingTopups(intent.userId)
  if (!entries.some((entry) => entry.attemptId === intent.attemptId)) return
  save(
    intent.userId,
    entries.map((entry) =>
      entry.attemptId === intent.attemptId ? { ...entry, tradeNo } : entry
    )
  )
}

export function forgetTopup(intent: PendingTopupCloud) {
  save(
    intent.userId,
    readPendingTopups(intent.userId).filter(
      (entry) => entry.attemptId !== intent.attemptId
    )
  )
  if (prepared?.attemptId === intent.attemptId) prepared = null
}
