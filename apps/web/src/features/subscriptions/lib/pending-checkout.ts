/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { UserSubscriptionRecord } from '../types'

export const SUBSCRIPTION_CHECKOUT_POLL_INTERVAL_MS = 3_000
export const SUBSCRIPTION_CHECKOUT_POLL_TIMEOUT_MS = 2 * 60 * 1_000

export type PendingSubscriptionCheckout = {
  baseline: string
  expiresAt: number
}

export function subscriptionCheckoutFingerprint(
  subscriptions: UserSubscriptionRecord[]
): string {
  return subscriptions
    .map(({ subscription }) =>
      [
        subscription.id,
        subscription.status,
        subscription.start_time,
        subscription.end_time,
        subscription.amount_total,
        subscription.amount_used,
        subscription.next_reset_time ?? 0,
      ].join(':')
    )
    .sort((left, right) => left.localeCompare(right))
    .join('|')
}

export function beginSubscriptionCheckoutConfirmation(
  baseline: string,
  now = Date.now()
): PendingSubscriptionCheckout {
  return {
    baseline,
    expiresAt: now + SUBSCRIPTION_CHECKOUT_POLL_TIMEOUT_MS,
  }
}

export function shouldContinueSubscriptionCheckoutConfirmation(
  pending: PendingSubscriptionCheckout,
  currentFingerprint: string,
  now = Date.now()
): boolean {
  return now < pending.expiresAt && currentFingerprint === pending.baseline
}
