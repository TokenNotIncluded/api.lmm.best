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
    paid_activation_min_amount?: number
    paid_activation_complete?: boolean
    details_available?: boolean
  }
  trust_level_info?: {
    overridden?: boolean
    paid_amount?: number
  }
}

export type L0PaidAccess = {
  mode: 'unknown' | 'review' | 'topup' | 'sync' | 'active'
  remaining: number
  threshold: number
  paid: number
}

/** Presentation only: balance, a URL and a payment redirect never grant access. */
export function getL0PaidAccess(
  user: L0AccessAccount | null | undefined
): L0PaidAccess {
  const empty = { remaining: 0, threshold: 0, paid: 0 }
  if (user?.developer_access_granted === true) {
    return { ...empty, mode: 'active' }
  }
  if (
    user?.trust_level_info?.overridden === true ||
    user?.onboarding?.paid_activation_enabled === false
  ) {
    return { ...empty, mode: 'review' }
  }
  const rawThreshold = user?.onboarding?.paid_activation_min_amount
  if (
    user?.onboarding?.paid_activation_enabled !== true ||
    user.onboarding.details_available === false ||
    typeof rawThreshold !== 'number' ||
    !Number.isFinite(rawThreshold) ||
    rawThreshold < 0
  ) {
    return { ...empty, mode: 'unknown' }
  }
  const rawPaid = user.trust_level_info?.paid_amount ?? 0
  if (!Number.isFinite(rawPaid) || rawPaid < 0) {
    return { ...empty, mode: 'unknown' }
  }
  // Match the backend's credited-USD micros, not settlement currency or wallet balance.
  const thresholdMicros = Math.round(rawThreshold * 1_000_000)
  const paidMicros = Math.round(rawPaid * 1_000_000)
  if (
    !Number.isSafeInteger(thresholdMicros) ||
    !Number.isSafeInteger(paidMicros)
  ) {
    return { ...empty, mode: 'unknown' }
  }
  const sync =
    user.onboarding.paid_activation_complete === true ||
    (paidMicros > 0 && paidMicros >= thresholdMicros)
  return {
    mode: sync ? 'sync' : 'topup',
    threshold: thresholdMicros / 1_000_000,
    paid: paidMicros / 1_000_000,
    remaining: Math.max(0, thresholdMicros - paidMicros) / 1_000_000,
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
