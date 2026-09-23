/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useCallback, useEffect, useRef, useState } from 'react'

import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { getUserBillingHistory, isApiSuccess } from '../api'
import type { WalletCloudSuccess } from '../components/wallet-token-cloud'
import { getTopupRecordPlatformAmount } from '../lib/payment'
import {
  findConfirmedTopup,
  type PendingTopupCloud,
} from '../lib/topup-cloud-success'

const STORAGE_KEY = 'wallet-pending-topup-cloud'
const WATCH_DURATION_MS = 15 * 60 * 1000
const POLL_INTERVAL_MS = 5_000

function readPending(userId: number | null): PendingTopupCloud | null {
  if (userId === null) return null
  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const value = JSON.parse(raw) as PendingTopupCloud
    if (
      value.userId !== userId ||
      !Number.isFinite(value.launchedAt) ||
      !Number.isFinite(value.expiresAt) ||
      !Number.isFinite(value.beforeQuota) ||
      !Number.isFinite(value.baselineSuccessId) ||
      !Number.isFinite(value.expectedCredit) ||
      value.expiresAt <= Date.now()
    ) {
      return null
    }
    return value
  } catch {
    return null
  }
}

function writePending(pending: PendingTopupCloud | null) {
  try {
    if (pending) {
      window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(pending))
    } else {
      window.sessionStorage.removeItem(STORAGE_KEY)
    }
  } catch {
    // Browser storage can be unavailable; polling still works in this tab.
  }
}

export function useTopupCloudSuccess({
  userId,
  quotaPerUnit,
  refreshUser,
  disabled,
}: {
  userId: number | null
  quotaPerUnit: number
  refreshUser: () => Promise<AuthUser | null>
  disabled: boolean
}) {
  const [pending, setPending] = useState<PendingTopupCloud | null>(null)
  const [success, setSuccess] = useState<WalletCloudSuccess | null>(null)
  const preparedRef = useRef<PendingTopupCloud | null>(null)
  const baselineSuccessIdRef = useRef(0)

  useEffect(() => {
    setSuccess(null)
    baselineSuccessIdRef.current = 0
    if (disabled) {
      setPending(null)
      return
    }
    const restored = readPending(userId)
    preparedRef.current = restored
    setPending(restored)
  }, [disabled, userId])

  const prefetchBaseline = useCallback(async () => {
    if (disabled || userId === null) return
    try {
      const response = await getUserBillingHistory(1, 20)
      if (!isApiSuccess(response) || !response.data) return
      if (useAuthStore.getState().auth.user?.id !== userId) return
      baselineSuccessIdRef.current = Math.max(
        baselineSuccessIdRef.current,
        ...(response.data.items ?? [])
          .filter((record) => record.status === 'success')
          .map((record) => record.id)
      )
    } catch {
      // The checkout can still proceed; the launch timestamp remains a guard.
    }
  }, [disabled, userId])

  const prepare = useCallback(
    (beforeQuota: number, expectedCredit: number) => {
      if (disabled || userId === null) return
      const launchedAt = Date.now()
      const intent: PendingTopupCloud = {
        userId,
        launchedAt,
        expiresAt: launchedAt + WATCH_DURATION_MS,
        baselineSuccessId: baselineSuccessIdRef.current,
        beforeQuota,
        expectedCredit,
      }
      preparedRef.current = intent
      writePending(intent)
    },
    [disabled, userId]
  )

  const activate = useCallback(() => {
    const intent = preparedRef.current ?? readPending(userId)
    if (intent && !disabled) setPending(intent)
  }, [disabled, userId])

  const cancel = useCallback(() => {
    preparedRef.current = null
    setPending(null)
    writePending(null)
  }, [])

  useEffect(() => {
    if (!pending || disabled || pending.userId !== userId) return
    let cancelled = false
    let inFlight = false

    const poll = async () => {
      if (cancelled || inFlight || document.visibilityState !== 'visible') {
        return
      }
      if (Date.now() >= pending.expiresAt) {
        cancel()
        return
      }
      inFlight = true
      try {
        const response = await getUserBillingHistory(1, 20)
        if (cancelled || !isApiSuccess(response) || !response.data) {
          return
        }
        const record = findConfirmedTopup(response.data.items ?? [], pending)
        if (!record) return
        const freshUser = await refreshUser()
        if (
          cancelled ||
          !freshUser ||
          freshUser.id !== pending.userId ||
          useAuthStore.getState().auth.user?.id !== pending.userId
        ) {
          return
        }
        const creditedCredits = getTopupRecordPlatformAmount(record)
        const credited =
          Number.isFinite(creditedCredits) && creditedCredits > 0
            ? creditedCredits
            : pending.expectedCredit
        if (credited <= 0) return
        setSuccess({
          orderId: record.id,
          beforeCredits: Math.max(0, pending.beforeQuota / quotaPerUnit),
          creditedCredits: credited,
        })
        cancel()
      } catch {
        // A transient read failure never turns an unconfirmed order into success.
      } finally {
        inFlight = false
      }
    }

    const onVisible = () => {
      if (document.visibilityState === 'visible') void poll()
    }
    void poll()
    const interval = window.setInterval(() => void poll(), POLL_INTERVAL_MS)
    window.addEventListener('focus', onVisible)
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      cancelled = true
      window.clearInterval(interval)
      window.removeEventListener('focus', onVisible)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [cancel, disabled, pending, quotaPerUnit, refreshUser, userId])

  return { success, prefetchBaseline, prepare, activate, cancel }
}
