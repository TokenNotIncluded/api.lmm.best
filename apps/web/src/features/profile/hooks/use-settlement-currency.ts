/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { updateSettlementCurrency } from '../api'
import {
  parseSettlementSettings,
  settlementCurrencyPreference,
} from '../lib/settlement-currency'
import type { SettlementCurrencyPreference, UserProfile } from '../types'

type Auth = ReturnType<typeof useAuthStore.getState>['auth']

function sameOwner(current: Auth, original: Auth) {
  return (
    current.user?.id === original.user?.id &&
    current.session?.sid === original.session?.sid &&
    current.accessToken === original.accessToken
  )
}

export function useSettlementCurrency({
  profile,
  loading,
  onProfileUpdate,
}: {
  profile: UserProfile | null
  loading: boolean
  onProfileUpdate: () => void | Promise<void>
}) {
  const queryClient = useQueryClient()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const source = settlementCurrencyPreference(
    parseSettlementSettings(profile?.setting).settlement_currency
  )
  // Keep an acknowledged write visible until profile refresh catches up. A
  // language-only refresh with the old preference must not undo that write.
  const [confirmed, setConfirmed] = useState<{
    source: SettlementCurrencyPreference
    value: SettlementCurrencyPreference
  } | null>(null)
  const savedValue = confirmed?.source === source ? confirmed.value : source
  const [pendingValue, setPendingValue] =
    useState<SettlementCurrencyPreference | null>(null)
  const [failedValue, setFailedValue] =
    useState<SettlementCurrencyPreference | null>(null)
  const [saved, setSaved] = useState(false)
  const active = useRef<AbortController | null>(null)
  const available = Boolean(userId && profile?.id === userId) && !loading
  const saving = pendingValue !== null

  useEffect(
    () => () => {
      const request = active.current
      active.current = null
      request?.abort()
    },
    []
  )

  async function save(value: SettlementCurrencyPreference) {
    if (!available || active.current || value === savedValue) return
    const owner = useAuthStore.getState().auth
    if (!owner.user || owner.user.id !== profile?.id) return

    const request = new AbortController()
    active.current = request
    const isCurrent = () =>
      !request.signal.aborted && sameOwner(useAuthStore.getState().auth, owner)
    // Also invalidate a switch away and back that React batches into one render.
    const unsubscribe = useAuthStore.subscribe(({ auth }) => {
      if (!sameOwner(auth, owner)) request.abort()
    })
    request.signal.addEventListener('abort', unsubscribe, { once: true })
    setPendingValue(value)
    setFailedValue(null)
    setSaved(false)

    try {
      const response = await updateSettlementCurrency(value, request.signal)
      if (!isCurrent()) return
      if (!response.success) throw new Error('Settlement preference not saved')

      // Check immediately before the merge, and read the latest same-owner
      // record: concurrent language/other settings updates are not ours to undo.
      const latest = useAuthStore.getState().auth
      if (!sameOwner(latest, owner) || !latest.user) return
      latest.setUser({
        ...latest.user,
        setting: JSON.stringify({
          ...parseSettlementSettings(latest.user.setting),
          settlement_currency: value,
        }),
      })
      setConfirmed({ source, value })
      setSaved(true)
      void queryClient.invalidateQueries({ queryKey: ['subscription-plans'] })
      void queryClient.invalidateQueries({ queryKey: ['topup-info'] })
      // Refresh is not part of the write: a failed read cannot undo its ACK.
      void Promise.resolve(onProfileUpdate()).catch(() => undefined)
    } catch {
      if (isCurrent()) setFailedValue(value)
    } finally {
      unsubscribe()
      request.signal.removeEventListener('abort', unsubscribe)
      if (active.current === request) {
        active.current = null
        setPendingValue(null)
      }
    }
  }

  return {
    value: pendingValue ?? savedValue,
    available,
    saving,
    saved,
    failedValue,
    save,
  }
}
