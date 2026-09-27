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
  TOPUP_CLOUD_EVENT,
  topupCloudStorageKey,
  readPendingTopups,
  prepareTopup,
  forgetTopup,
} from '../lib/topup-cloud-storage'
import {
  findConfirmedTopup,
  type PendingTopupCloud,
} from '../lib/topup-cloud-success'

const POLL_INTERVAL_MS = 5_000
const ACTIVE_WATCH_MS = 15 * 60 * 1000

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
  const [pending, setPending] = useState<PendingTopupCloud[]>([])
  const [success, setSuccess] = useState<WalletCloudSuccess | null>(null)
  const preparedRef = useRef<PendingTopupCloud | null>(null)
  const confirmedRef = useRef<PendingTopupCloud | null>(null)

  const sync = useCallback(() => {
    const entries = disabled || userId === null ? [] : readPendingTopups(userId)
    setPending((previous) =>
      JSON.stringify(previous) === JSON.stringify(entries) ? previous : entries
    )
    const confirmed = confirmedRef.current
    if (
      confirmed &&
      !entries.some((entry) => entry.attemptId === confirmed.attemptId)
    ) {
      confirmedRef.current = null
      setSuccess(null)
    }
  }, [disabled, userId])

  useEffect(() => {
    preparedRef.current = null
    confirmedRef.current = null
    setSuccess(null)
    sync()
    const onStorage = (event: StorageEvent) => {
      if (
        event.key === null ||
        (userId !== null && event.key === topupCloudStorageKey(userId))
      ) {
        sync()
      }
    }
    window.addEventListener(TOPUP_CLOUD_EVENT, sync)
    window.addEventListener('storage', onStorage)
    window.addEventListener('focus', sync)
    return () => {
      window.removeEventListener(TOPUP_CLOUD_EVENT, sync)
      window.removeEventListener('storage', onStorage)
      window.removeEventListener('focus', sync)
    }
  }, [sync, userId])

  const prepare = useCallback(
    (beforeQuota: number, expectedCredit: number) => {
      if (!disabled && userId !== null) {
        preparedRef.current = prepareTopup(userId, beforeQuota, expectedCredit)
      }
    },
    [disabled, userId]
  )

  const cancel = useCallback(() => {
    if (preparedRef.current) forgetTopup(preparedRef.current)
    preparedRef.current = null
  }, [])

  const acknowledge = useCallback(
    (orderId: number) => {
      if (success?.orderId !== orderId || !confirmedRef.current) return
      // Only consume after the cloud has actually been shown. A reload or route
      // change before that point will recheck the same server order, not lose it.
      forgetTopup(confirmedRef.current)
      confirmedRef.current = null
      setSuccess(null)
    },
    [success]
  )

  useEffect(() => {
    if (
      disabled ||
      userId === null ||
      success ||
      !pending.some((entry) => entry.tradeNo)
    ) {
      return
    }
    let cancelled = false
    let inFlight = false
    let watchUntil = Date.now() + ACTIVE_WATCH_MS
    const poll = async () => {
      if (
        cancelled ||
        inFlight ||
        document.visibilityState !== 'visible' ||
        Date.now() > watchUntil
      ) {
        return
      }
      inFlight = true
      try {
        for (const intent of pending) {
          if (intent.userId !== userId || !intent.tradeNo) continue
          if (Date.now() >= intent.expiresAt) {
            forgetTopup(intent)
            continue
          }
          // Keyword query avoids losing an older order behind the first 20 rows.
          const response = await getUserBillingHistory(1, 20, intent.tradeNo)
          if (cancelled || useAuthStore.getState().auth.user?.id !== userId) {
            return
          }
          if (
            response.success === false ||
            !isApiSuccess(response) ||
            !response.data
          ) {
            continue
          }
          const record = findConfirmedTopup(response.data.items ?? [], intent)
          if (!record) continue
          const freshUser = await refreshUser()
          if (
            cancelled ||
            !freshUser ||
            freshUser.id !== userId ||
            useAuthStore.getState().auth.user?.id !== userId
          ) {
            return
          }
          if (
            !readPendingTopups(userId).some(
              (entry) => entry.attemptId === intent.attemptId
            )
          ) {
            continue
          }
          const credited = getTopupRecordPlatformAmount(record)
          if (!Number.isFinite(credited) || credited <= 0) continue
          confirmedRef.current = intent
          setSuccess({
            orderId: record.id,
            beforeCredits: Math.max(0, intent.beforeQuota / quotaPerUnit),
            creditedCredits: credited,
          })
          return
        }
      } catch {
        // Transient read failures cannot turn an unconfirmed payment into success.
      } finally {
        inFlight = false
      }
    }
    const onVisible = () => {
      if (document.visibilityState === 'visible') {
        watchUntil = Date.now() + ACTIVE_WATCH_MS
        void poll()
      }
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
  }, [disabled, pending, quotaPerUnit, refreshUser, success, userId])

  return { success, prepare, activate: sync, cancel, acknowledge }
}
