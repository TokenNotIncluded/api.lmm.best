/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
export const SUBSCRIPTION_CHECKOUT_POLL_INTERVAL_MS = 3_000
export const SUBSCRIPTION_CHECKOUT_POLL_TIMEOUT_MS = 2 * 60 * 1_000

export type SubscriptionCheckoutReference = {
  tradeNo: string
  userId: number
  planId: number
}

export type PendingSubscriptionCheckout = SubscriptionCheckoutReference & {
  expiresAt: number
  attempt: number
  state: 'pending' | 'timed_out' | 'cancelled' | 'failed' | 'confirmed'
}

export type SubscriptionCheckoutEvidence = {
  trade_no: string
  user_id: number
  plan_id: number
  payment_status: string
  complete_time: number
  user_subscription_id: number
  confirmed: boolean
}

function positiveId(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

export function subscriptionCheckoutTradeNo(
  data: unknown,
  formPayment = false
): string | undefined {
  if (!data || typeof data !== 'object') return undefined
  const fields = data as Record<string, unknown>
  const value = fields[formPayment ? 'out_trade_no' : 'order_id']
  return typeof value === 'string' &&
    value.length > 0 &&
    value.length <= 255 &&
    value.trim() === value
    ? value
    : undefined
}

export function beginSubscriptionCheckoutConfirmation(
  reference: SubscriptionCheckoutReference,
  now = Date.now()
): PendingSubscriptionCheckout {
  if (
    !subscriptionCheckoutTradeNo({ order_id: reference.tradeNo }) ||
    !positiveId(reference.userId) ||
    !positiveId(reference.planId)
  ) {
    throw new Error('Subscription checkout requires an order reference')
  }
  return {
    ...reference,
    expiresAt: now + SUBSCRIPTION_CHECKOUT_POLL_TIMEOUT_MS,
    attempt: 1,
    state: 'pending',
  }
}

function matches(
  pending: PendingSubscriptionCheckout,
  evidence: SubscriptionCheckoutEvidence | undefined
): evidence is SubscriptionCheckoutEvidence {
  return !!evidence &&
    evidence.trade_no === pending.tradeNo &&
    evidence.user_id === pending.userId &&
    evidence.plan_id === pending.planId
}

export function subscriptionCheckoutState(
  pending: PendingSubscriptionCheckout,
  evidence?: SubscriptionCheckoutEvidence,
  now = Date.now()
): PendingSubscriptionCheckout['state'] {
  // A display refresh, a new subscription ID, or a paid order without its
  // linked grant is not proof of this checkout. Confirmation is read-only.
  if (pending.state === 'confirmed') return 'confirmed'
  if (matches(pending, evidence)) {
    if (
      evidence.confirmed === true &&
      evidence.payment_status === 'success' &&
      Number.isSafeInteger(evidence.complete_time) &&
      evidence.complete_time > 0 &&
      positiveId(evidence.user_subscription_id)
    ) return 'confirmed'
    if (evidence.payment_status === 'cancelled' || evidence.payment_status === 'expired') {
      return 'cancelled'
    }
    if (evidence.payment_status === 'failed' || evidence.payment_status === 'refunded') {
      return 'failed'
    }
  }
  if (pending.state !== 'pending') return pending.state
  return now < pending.expiresAt ? 'pending' : 'timed_out'
}

export function shouldContinueSubscriptionCheckoutConfirmation(
  pending: PendingSubscriptionCheckout,
  evidence?: SubscriptionCheckoutEvidence,
  now = Date.now()
): boolean {
  return subscriptionCheckoutState(pending, evidence, now) === 'pending'
}

export function applySubscriptionCheckoutEvidence(
  checkouts: PendingSubscriptionCheckout[],
  request: PendingSubscriptionCheckout,
  evidence?: SubscriptionCheckoutEvidence,
  now = Date.now()
): PendingSubscriptionCheckout[] {
  let changed = false
  const next = checkouts.map((current) => {
    if (current.tradeNo !== request.tradeNo ||
      current.userId !== request.userId || current.planId !== request.planId ||
      current.attempt !== request.attempt) return current
    const state = subscriptionCheckoutState(current, evidence, now)
    if (state === current.state) return current
    changed = true
    return { ...current, state }
  })
  return changed ? next : checkouts
}

export function retrySubscriptionCheckoutConfirmation(
  pending: PendingSubscriptionCheckout,
  now = Date.now()
): PendingSubscriptionCheckout {
  if (pending.state === 'confirmed') return pending
  return { ...pending, state: 'pending', attempt: pending.attempt + 1,
    expiresAt: now + SUBSCRIPTION_CHECKOUT_POLL_TIMEOUT_MS }
}

export function restoreSubscriptionCheckouts(
  stored: string | null,
  userId: number | undefined,
  now = Date.now()
): PendingSubscriptionCheckout[] {
  if (!stored || !positiveId(userId)) return []
  try {
    const parsed = JSON.parse(stored)
    // Fingerprint-only v1 markers cannot identify an order. Never infer one
    // from the newest subscription, plan, payment return URL, or timestamp.
    if (parsed?.version !== 2 || !Array.isArray(parsed.checkouts)) return []
    const restored: PendingSubscriptionCheckout[] = []
    for (const value of parsed.checkouts) {
      if (!value || value.userId !== userId || !positiveId(value.planId) ||
        !subscriptionCheckoutTradeNo({ order_id: value.tradeNo }) ||
        !Number.isSafeInteger(value.expiresAt) || value.expiresAt <= 0 ||
        !positiveId(value.attempt) ||
        !['pending', 'timed_out', 'cancelled', 'failed', 'confirmed'].includes(value.state) ||
        restored.some((item) => item.tradeNo === value.tradeNo)) continue
      const pending: PendingSubscriptionCheckout = {
        tradeNo: value.tradeNo, userId, planId: value.planId,
        expiresAt: value.expiresAt, attempt: value.attempt, state: value.state,
      }
      restored.push({ ...pending, state: subscriptionCheckoutState(pending, undefined, now) })
    }
    return restored
  } catch {
    return []
  }
}
