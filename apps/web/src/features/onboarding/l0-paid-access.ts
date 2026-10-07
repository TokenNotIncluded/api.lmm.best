/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
export type L0AccessAccount = {
  id: number
  developer_access_granted?: boolean
  onboarding?: {
    paid_activation_enabled?: boolean
    paid_activation_min_credits?: string
    paid_activation_min_amount?: number
    paid_activation_complete?: boolean
    details_available?: boolean
  }
  trust_level_info?: {
    overridden?: boolean
    paid_amount?: number
    paid_credits?: string | null
    paid_credit_projection_available?: boolean
  }
  trust_level_tiers?: Array<{ level: number; min_paid_credits?: string }>
}

export type L0PaidAccess = {
  mode: 'unknown' | 'review' | 'topup' | 'sync' | 'active'
  remainingCredits: number
  thresholdCredits: number
  paidCredits: number
}

function exactCredits(value: unknown): number | null {
  if (typeof value !== 'string' || !/^\d+$/.test(value)) return null
  const credits = BigInt(value)
  return credits <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(credits) : null
}

/** Presentation only: balance, a URL and a payment redirect never grant access. */
export function getL0PaidAccess(
  user: L0AccessAccount | null | undefined
): L0PaidAccess {
  const empty = { remainingCredits: 0, thresholdCredits: 0, paidCredits: 0 }
  if (user?.developer_access_granted === true) {
    return { ...empty, mode: 'active' }
  }
  if (
    user?.trust_level_info?.overridden === true ||
    user?.onboarding?.paid_activation_enabled === false
  ) {
    return { ...empty, mode: 'review' }
  }
  if (
    user?.onboarding?.paid_activation_enabled !== true ||
    user.onboarding.details_available === false ||
    user.trust_level_info?.paid_credit_projection_available === false
  ) {
    return { ...empty, mode: 'unknown' }
  }
  // Older current servers already expose the configured L1 credit threshold
  // in tier views. Never interpret historical policy amounts as USD or credits.
  const thresholdCredits = exactCredits(
    user.onboarding.paid_activation_min_credits ??
      user.trust_level_tiers?.find((tier) => tier.level === 1)?.min_paid_credits
  )
  const paidCredits = exactCredits(user.trust_level_info?.paid_credits)
  if (thresholdCredits === null || paidCredits === null) {
    return { ...empty, mode: 'unknown' }
  }
  const sync =
    user.onboarding.paid_activation_complete === true ||
    (paidCredits > 0 && paidCredits >= thresholdCredits)
  return {
    mode: sync ? 'sync' : 'topup',
    thresholdCredits,
    paidCredits,
    remainingCredits: Math.max(0, thresholdCredits - paidCredits),
  }
}

export type L0AccessCheckState = 'checking' | 'waiting' | 'error' | 'timeout'

/** A bounded, user-started read loop. Hidden pages wait without doing requests. */
export function watchL0Access({
  userId,
  read,
  report,
  target = document,
  interval = 3_000,
  duration = 120_000,
}: {
  userId: number
  read: () => Promise<L0AccessAccount | null>
  report: (state: L0AccessCheckState) => void
  target?: Pick<Document, 'hidden' | 'addEventListener' | 'removeEventListener'>
  interval?: number
  duration?: number
}): () => void {
  let stopped = false
  let running = false
  let timer: ReturnType<typeof setTimeout> | undefined
  const deadline = Date.now() + duration
  const stop = () => {
    stopped = true
    clearTimeout(timer)
    target.removeEventListener('visibilitychange', wake)
  }
  const poll = async () => {
    if (stopped || running) return
    clearTimeout(timer)
    if (Date.now() >= deadline) {
      report('timeout')
      stop()
      return
    }
    if (target.hidden) return
    running = true
    report('checking')
    try {
      const user = await read()
      if (stopped) return
      if (user && user.id !== userId) {
        stop()
        return
      }
      if (user?.developer_access_granted === true) {
        // refreshCurrentAccount already committed the authoritative server response.
        stop()
        return
      }
      report(user ? 'waiting' : 'error')
    } catch {
      if (!stopped) report('error')
    } finally {
      running = false
      if (!stopped) timer = setTimeout(() => void poll(), interval)
    }
  }
  function wake() {
    if (!target.hidden) void poll()
  }
  target.addEventListener('visibilitychange', wake)
  void poll()
  return stop
}
