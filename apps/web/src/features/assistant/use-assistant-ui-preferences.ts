/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useMemo, useRef } from 'react'

import { useThemeCustomization } from '@/context/theme-customization-provider'
import { useTheme } from '@/context/theme-provider'
import appI18n from '@/i18n/config'
import { normalizeInterfaceLanguage } from '@/i18n/languages'
import {
  normalizeWalletDisplayCurrencyPreference,
  parseWalletCurrencySettings,
} from '@/lib/currency'
import { api } from '@/lib/http-client'
import { useAuthStore } from '@/stores/auth-store'

import type { UIPreferencePatch, UIPreferences } from './assistant-ui-preferences-contract'
import type { UIPreferenceAdapter } from './assistant-ui-preferences-runtime'

export function useAssistantUIPreferenceAdapter(): UIPreferenceAdapter {
  const theme = useTheme()
  const customization = useThemeCustomization()
  const latest = useRef({ theme, customization })
  latest.current = { theme, customization }

  return useMemo(() => {
    const owner = () => {
      const auth = useAuthStore.getState().auth
      return auth.user && auth.session?.sid
        ? { userID: auth.user.id, sessionID: auth.session.sid }
        : undefined
    }
    const read = (): UIPreferences => {
      const user = useAuthStore.getState().auth.user
      return {
        mode: latest.current.theme.theme,
        theme: latest.current.customization.customization.preset,
        language: normalizeInterfaceLanguage(appI18n.language),
        currency: normalizeWalletDisplayCurrencyPreference(
          parseWalletCurrencySettings(user?.setting).wallet_display_currency
        ) || 'auto',
      }
    }
    const save = async (patch: UIPreferencePatch, signal: AbortSignal) => {
      const original = useAuthStore.getState().auth
      const before = read()
      const request = new AbortController()
      const abort = () => request.abort()
      const isCurrent = () => {
        const current = useAuthStore.getState().auth
        return !request.signal.aborted && !!original.user && !!original.session?.sid &&
          current.user?.id === original.user.id &&
          current.session?.sid === original.session.sid &&
          current.accessToken === original.accessToken
      }
      const check = () => {
        if (!isCurrent()) throw new Error('Preference session changed')
      }
      if (signal.aborted) abort()
      signal.addEventListener('abort', abort, { once: true })
      const unsubscribe = useAuthStore.subscribe(() => {
        if (!isCurrent()) abort()
      })
      try {
        check()
        if (patch.language) {
          await appI18n.loadLanguages(patch.language)
          check()
        }
        const settings: { language?: string; wallet_display_currency?: string } = {}
        if (patch.language) settings.language = patch.language
        if (patch.currency) {
          settings.wallet_display_currency = patch.currency === 'auto' ? '' : patch.currency
        }
        if (Object.keys(settings).length > 0) {
          // One existing profile request, containing only the changed fields.
          // No settlement currency, balances or exchange rates are submitted.
          const response = await api.put('/api/user/self', settings, {
            signal: request.signal,
            skipBusinessError: true,
            skipErrorHandler: true,
          })
          check()
          if (response.data?.success !== true) throw new Error('Preference save failed')
        }
        const currentPreferences = read()
        for (const key of Object.keys(patch) as Array<keyof UIPreferences>) {
          if (currentPreferences[key] !== before[key] && currentPreferences[key] !== patch[key]) {
            throw new Error('A newer manual preference must be kept')
          }
        }
        check()
        const current = useAuthStore.getState().auth
        if (Object.keys(settings).length > 0 && current.user) {
          current.setUser({
            ...current.user,
            setting: JSON.stringify({
              ...parseWalletCurrencySettings(current.user.setting),
              ...settings,
            }),
          })
        }
        if (patch.language) {
          check()
          await appI18n.changeLanguage(patch.language)
          check()
        }
        if (patch.mode) {
          latest.current.theme.setTheme(patch.mode)
          latest.current.theme = { ...latest.current.theme, theme: patch.mode }
        }
        if (patch.theme) {
          latest.current.customization.setPreset(patch.theme)
          latest.current.customization = {
            ...latest.current.customization,
            customization: { ...latest.current.customization.customization, preset: patch.theme },
          }
        }
      } finally {
        unsubscribe()
        signal.removeEventListener('abort', abort)
      }
    }
    const preview = (patch: UIPreferencePatch) => {
      const releases: Array<() => void> = []
      const release = () => { for (const stop of releases.reverse()) stop() }
      try {
        if (patch.mode) releases.push(latest.current.theme.previewTheme(patch.mode))
        if (patch.theme) releases.push(latest.current.customization.previewPreset(patch.theme))
      } catch (error) {
        release()
        throw error
      }
      return release
    }
    return { owner, read, save, preview }
  }, [])
}
