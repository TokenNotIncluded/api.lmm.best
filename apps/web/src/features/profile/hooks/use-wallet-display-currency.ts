/*
Copyright (C) 2026 LIghtJUNction
*/
import { useEffect, useRef, useState } from 'react'

import { useAuthStore } from '@/stores/auth-store'
import type { WalletDisplayCurrencyPreference } from '@/stores/wallet-currency-preference-store'

import { updateWalletDisplayCurrency } from '../api'
import { parseSettlementSettings } from '../lib/settlement-currency'
import {
  sameWalletDisplayOwner,
  walletDisplayCurrencyPreference,
  walletDisplayOwnerKey,
} from '../lib/wallet-display-currency'
import type { UserProfile } from '../types'

type PreferenceState = {
  ownerKey: string
  confirmed: {
    source: WalletDisplayCurrencyPreference
    value: WalletDisplayCurrencyPreference
  } | null
  pendingValue: WalletDisplayCurrencyPreference | null
  failedValue: WalletDisplayCurrencyPreference | null
  saved: boolean
}

function emptyState(ownerKey: string): PreferenceState {
  return {
    ownerKey,
    confirmed: null,
    pendingValue: null,
    failedValue: null,
    saved: false,
  }
}

export function useWalletDisplayCurrency({
  profile,
  loading,
  onProfileUpdate,
}: {
  profile: UserProfile | null
  loading: boolean
  onProfileUpdate: () => void | Promise<void>
}) {
  const ownerKey = useAuthStore(({ auth }) => walletDisplayOwnerKey(auth))
  const userId = useAuthStore(({ auth }) => auth.user?.id)
  const sameProfileOwner = Boolean(userId && profile?.id === userId)
  const source = sameProfileOwner
    ? walletDisplayCurrencyPreference(profile?.setting)
    : ''
  const [state, setState] = useState<PreferenceState>(() =>
    emptyState(ownerKey)
  )
  // Mismatched owner state is hidden immediately, before effects can run.
  const current = state.ownerKey === ownerKey ? state : emptyState(ownerKey)
  const savedValue =
    current.confirmed?.source === source ? current.confirmed.value : source
  const active = useRef<AbortController | null>(null)
  const available = sameProfileOwner && !loading

  useEffect(() => {
    setState(emptyState(ownerKey))
    return () => {
      const request = active.current
      active.current = null
      request?.abort()
    }
  }, [ownerKey])

  async function save(value: WalletDisplayCurrencyPreference) {
    if (!available || active.current || value === savedValue) return
    const owner = useAuthStore.getState().auth
    if (
      !owner.user ||
      owner.user.id !== profile?.id ||
      walletDisplayOwnerKey(owner) !== ownerKey
    ) {
      return
    }
    const request = new AbortController()
    active.current = request
    const isCurrent = () =>
      !request.signal.aborted &&
      sameWalletDisplayOwner(useAuthStore.getState().auth, owner)
    const unsubscribe = useAuthStore.subscribe(({ auth }) => {
      if (!sameWalletDisplayOwner(auth, owner)) {
        if (active.current === request) active.current = null
        setState(emptyState(walletDisplayOwnerKey(auth)))
        request.abort()
      }
    })
    request.signal.addEventListener('abort', unsubscribe, { once: true })
    setState((previous) => ({
      ...(previous.ownerKey === ownerKey ? previous : emptyState(ownerKey)),
      pendingValue: value,
      failedValue: null,
      saved: false,
    }))

    try {
      const response = await updateWalletDisplayCurrency(value, request.signal)
      if (!isCurrent()) return
      if (!response.success) {
        throw new Error('Wallet display preference not saved')
      }
      const latest = useAuthStore.getState().auth
      if (!sameWalletDisplayOwner(latest, owner) || !latest.user) return
      latest.setUser({
        ...latest.user,
        setting: JSON.stringify({
          ...parseSettlementSettings(latest.user.setting),
          wallet_display_currency: value,
        }),
      })
      setState((previous) =>
        previous.ownerKey === ownerKey
          ? {
              ...previous,
              confirmed: { source, value },
              failedValue: null,
              saved: true,
            }
          : previous
      )
      // Profile refresh is a separate read. Neither a thrown callback nor a
      // failed refresh can turn an acknowledged write into a failed save.
      void Promise.resolve()
        .then(onProfileUpdate)
        .catch(() => undefined)
    } catch {
      if (isCurrent()) {
        setState((previous) =>
          previous.ownerKey === ownerKey
            ? { ...previous, failedValue: value }
            : previous
        )
      }
    } finally {
      unsubscribe()
      request.signal.removeEventListener('abort', unsubscribe)
      if (active.current === request) {
        active.current = null
        setState((previous) =>
          previous.ownerKey === ownerKey
            ? { ...previous, pendingValue: null }
            : previous
        )
      }
    }
  }

  return {
    value: current.pendingValue ?? savedValue,
    available,
    saving: current.pendingValue !== null,
    saved: current.saved,
    failedValue: current.failedValue,
    save,
  }
}
