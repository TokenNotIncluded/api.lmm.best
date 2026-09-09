/*
Copyright (C) 2026 LIghtJUNction
*/
import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { useAuthStore } from '@/stores/auth-store'

function accountScope() {
  const { user, session } = useAuthStore.getState().auth
  let setting = user?.setting
  if (typeof setting === 'string') {
    try {
      setting = JSON.parse(setting)
    } catch {
      setting = undefined
    }
  }
  const preference =
    typeof setting === 'object' && setting !== null
      ? setting.settlement_currency
      : undefined
  return JSON.stringify([
    user?.id ?? null,
    session?.sid ?? null,
    preference ?? null,
    user?.language ?? null,
  ])
}

/** Invalidate local checkout state and cache identity on account/preference/locale changes. */
export function useCheckoutScope() {
  const { i18n } = useTranslation()
  const account = useAuthStore(accountScope)
  const language = i18n.resolvedLanguage || i18n.language
  const key = JSON.stringify([account, language])
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const isCurrent = useCallback(
    () =>
      mounted.current &&
      accountScope() === account &&
      (i18n.resolvedLanguage || i18n.language) === language,
    [account, i18n, language]
  )
  return { key, isCurrent }
}
