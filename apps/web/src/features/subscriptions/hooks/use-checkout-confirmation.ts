/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useCallback, useEffect, useRef, useState } from 'react'

import { useCheckoutScope } from '@/features/wallet/hooks/use-checkout-scope'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  SUBSCRIPTION_CHECKOUT_POLL_INTERVAL_MS,
  applySubscriptionCheckoutEvidence,
  beginSubscriptionCheckoutConfirmation,
  restoreSubscriptionCheckouts,
  retrySubscriptionCheckoutConfirmation,
  type PendingSubscriptionCheckout,
  type SubscriptionCheckoutEvidence,
} from '../lib/pending-checkout'

export function useSubscriptionCheckoutConfirmation(
  userId: number | undefined,
  onConfirmed: () => void | Promise<void>
) {
  const { isCurrent } = useCheckoutScope()
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const authenticatedUserId = useAuthStore((state) => state.auth.user?.id)
  const storageKey = userId && userId === authenticatedUserId
    ? `subscription-checkout-confirmation:${userId}`
    : undefined
  const [checkouts, setCheckouts] = useState<PendingSubscriptionCheckout[]>(() => {
    try {
      return restoreSubscriptionCheckouts(
        storageKey ? window.sessionStorage.getItem(storageKey) : null, userId
      )
    } catch {
      return []
    }
  })
  const current = useRef(checkouts)
  const callback = useRef(onConfirmed)
  useEffect(() => { callback.current = onConfirmed }, [onConfirmed])
  const requests = useRef(new Map<string, { attempt: number; controller: AbortController }>())

  const save = useCallback((next: PendingSubscriptionCheckout[]) => {
    if (!isCurrent() || next === current.current) return
    current.current = next
    // Write synchronously: Pancake navigates this tab immediately afterward.
    try {
      if (storageKey) window.sessionStorage.setItem(storageKey, JSON.stringify({ version: 2, checkouts: next }))
    } catch {
      // Storage can be unavailable; memory confirmation still works.
    }
    setCheckouts(next)
  }, [isCurrent, storageKey])

  const check = useCallback(async (request: PendingSubscriptionCheckout) => {
    if (!isCurrent() || request.state === 'confirmed') return
    const previous = requests.current.get(request.tradeNo)
    if (previous?.attempt === request.attempt) return
    previous?.controller.abort()
    const controller = new AbortController()
    const running = { attempt: request.attempt, controller }
    requests.current.set(request.tradeNo, running)
    // A hanging request must not retain the in-flight slot indefinitely.
    const timeout = window.setTimeout(() => {
      controller.abort()
      if (requests.current.get(request.tradeNo) === running) {
        requests.current.delete(request.tradeNo)
      }
    }, 10_000)
    try {
      const res = await api.get('/api/subscription/self', {
        params: { checkout_trade_no: request.tradeNo },
        signal: controller.signal,
        authScope: { userId, sessionId },
        timeout: 10_000,
        skipBusinessError: true,
        skipErrorHandler: true,
      })
      if (!isCurrent() || controller.signal.aborted || !res.data?.success) return
      const before = current.current
      const next = applySubscriptionCheckoutEvidence(
        before, request, res.data.data as SubscriptionCheckoutEvidence
      )
      const confirmed = next.some((item, index) =>
        item.state === 'confirmed' && before[index]?.state !== 'confirmed'
      )
      save(next)
      // Persist first so repeated responses/reloads cannot notify twice.
      if (confirmed) await callback.current()
    } catch {
      // Unknown is not failed or paid. Keep the order for timeout/recovery.
    } finally {
      window.clearTimeout(timeout)
      if (requests.current.get(request.tradeNo) === running) {
        requests.current.delete(request.tradeNo)
      }
    }
  }, [isCurrent, save, sessionId, userId])

  const start = useCallback((tradeNo: string, planId: number) => {
    if (!isCurrent() || !userId || userId !== authenticatedUserId) return false
    if (current.current.some((item) => item.tradeNo === tradeNo)) return true
    const pending = beginSubscriptionCheckoutConfirmation({ tradeNo, planId, userId })
    save([...current.current, pending])
    void check(pending)
    return true
  }, [authenticatedUserId, check, isCurrent, save, userId])

  const retry = useCallback((tradeNo?: string) => {
    if (!isCurrent()) return
    const next = current.current.map((item) =>
      !tradeNo || item.tradeNo === tradeNo
        ? retrySubscriptionCheckoutConfirmation(item)
        : item
    )
    save(next)
    for (const item of next) {
      if (!tradeNo || item.tradeNo === tradeNo) void check(item)
    }
  }, [check, isCurrent, save])

  useEffect(() => {
    // Returning from payment or reloading checks the saved identities once,
    // including timed-out orders, without inferring anything from a return URL.
    for (const item of current.current) void check(item)
    const inFlight = requests.current
    return () => {
      for (const request of inFlight.values()) request.controller.abort()
      inFlight.clear()
    }
  }, [check])

  const hasPending = checkouts.some((item) => item.state === 'pending')
  const hasUnconfirmed = checkouts.some((item) => item.state !== 'confirmed')
  useEffect(() => {
    const refresh = (recover = false) => {
      if (!isCurrent()) return
      let next = current.current
      for (const item of current.current) {
        next = applySubscriptionCheckoutEvidence(next, item)
      }
      save(next)
      if (document.visibilityState === 'visible') {
        for (const item of next) {
          if (item.state === 'pending' || (recover && item.state !== 'confirmed')) void check(item)
        }
      }
    }
    // Timeout processing does not await a request and runs even in a hidden tab.
    if (!hasUnconfirmed) return
    const recover = () => refresh(true)
    const timer = hasPending
      ? window.setInterval(() => refresh(), SUBSCRIPTION_CHECKOUT_POLL_INTERVAL_MS)
      : undefined
    // Returning from a long payment checks even a timed-out order once.
    // It does not restart an unbounded polling loop or create another charge.
    window.addEventListener('focus', recover)
    document.addEventListener('visibilitychange', recover)
    return () => {
      if (timer !== undefined) window.clearInterval(timer)
      window.removeEventListener('focus', recover)
      document.removeEventListener('visibilitychange', recover)
    }
  }, [check, hasPending, hasUnconfirmed, isCurrent, save])

  return { checkouts, start, retry }
}
