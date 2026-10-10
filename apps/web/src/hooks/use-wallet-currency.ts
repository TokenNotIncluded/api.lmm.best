/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import {
  formatCumulativeUserUsage,
  type CumulativeUserUsage,
} from '@/lib/cumulative-user-usage'
import {
  displayAmountToQuota,
  formatQuotaInCurrency,
  formatExactQuotaInCurrency,
  formatAmountInCurrency,
  formatUSDInCurrency,
  getCurrencyDisplay,
  getCurrencyFormattingLocale,
  legacyPlatformAmountToQuota,
  normalizeWalletDisplayCurrencyPreference,
  parseWalletCurrencySettings,
  quotaToDisplayAmount,
  quotaToDisplayInput,
  quotaToLegacyPlatformAmount,
  resolveWalletDisplayCurrency,
  type CurrencyFormatOptions,
} from '@/lib/currency'
import { api } from '@/lib/http-client'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'
import {
  useWalletCurrencyPreferenceStore,
  type WalletDisplayCurrencyPreference,
} from '@/stores/wallet-currency-preference-store'

type Owner = ReturnType<typeof useAuthStore.getState>['auth']
const ownerKey = (owner: Owner) =>
  JSON.stringify([
    owner.user?.id ?? null,
    owner.session?.sid ?? '',
    owner.accessToken ?? '',
  ])

/** One reactive, captured denomination for labels, display and inverse input conversion. */
export function useWalletCurrency() {
  const { t, i18n } = useTranslation()
  const currentOwner = useAuthStore((state) => state.auth)
  const currencyConfig = useSystemConfigStore((state) => state.config.currency)
  const anonymousPreference = useWalletCurrencyPreferenceStore(
    (state) => state.preference
  )
  const language = i18n.resolvedLanguage || i18n.language
  const key = ownerKey(currentOwner)
  const preference = normalizeWalletDisplayCurrencyPreference(
    currentOwner.user
      ? parseWalletCurrencySettings(currentOwner.user.setting)
          .wallet_display_currency
      : anonymousPreference
  )
  const currency = resolveWalletDisplayCurrency(preference, language)
  const creditLabel = t(['credits', 'Credits'])
  const label = currency === 'CREDIT' ? creditLabel : currency
  const [pending, setPending] = useState<{
    key: string
    error: string | null
    saving: boolean
  } | null>(null)
  const active = useRef<AbortController | null>(null)

  useEffect(
    () => () => {
      active.current?.abort()
    },
    []
  )

  const setPreference = useCallback(
    async (value: WalletDisplayCurrencyPreference) => {
      const owner = useAuthStore.getState().auth
      if (!owner.user) {
        useWalletCurrencyPreferenceStore.getState().setPreference(value)
        return true
      }
      if (active.current && !active.current.signal.aborted) return false
      const request = new AbortController()
      active.current = request
      const originalKey = ownerKey(owner)
      const isCurrent = () =>
        !request.signal.aborted &&
        ownerKey(useAuthStore.getState().auth) === originalKey
      const unsubscribe = useAuthStore.subscribe(({ auth }) => {
        if (ownerKey(auth) !== originalKey) request.abort()
      })
      setPending({ key: originalKey, error: null, saving: true })
      try {
        const response = await api.put(
          '/api/user/self',
          { wallet_display_currency: value },
          {
            signal: request.signal,
            skipBusinessError: true,
            skipErrorHandler: true,
          }
        )
        if (!isCurrent()) return false
        if (response.data?.success !== true) {
          throw new Error('Could not save balance display currency. Try again.')
        }
        const latest = useAuthStore.getState().auth
        if (!latest.user || !isCurrent()) return false
        latest.setUser({
          ...latest.user,
          setting: JSON.stringify({
            ...parseWalletCurrencySettings(latest.user.setting),
            wallet_display_currency: value,
          }),
        })
        setPending({ key: originalKey, error: null, saving: false })
        return true
      } catch {
        if (isCurrent()) {
          setPending({
            key: originalKey,
            error: 'Could not save balance display currency. Try again.',
            saving: false,
          })
        }
        return false
      } finally {
        unsubscribe()
        if (active.current === request) active.current = null
      }
    },
    []
  )

  const snapshot = useMemo(() => {
    const config = getCurrencyDisplay(currencyConfig).config
    const displayLocale = getCurrencyFormattingLocale(language)
    const localized = (
      options?: CurrencyFormatOptions
    ): CurrencyFormatOptions => ({
      ...options,
      locale: options?.locale ?? displayLocale,
      creditLabel: options?.creditLabel ?? creditLabel,
    })
    return {
      config,
      currency,
      preference,
      label,
      step:
        currency === 'CREDIT' &&
        config.creditUnitSchemaVersion === undefined &&
        quotaToDisplayAmount(1, currency, config) === 1
          ? (1 as const)
          : ('any' as const),
      formatAmount: (amount: number, options?: CurrencyFormatOptions) =>
        formatAmountInCurrency(amount, currency, localized(options)),
      formatUserUsage: (user: CumulativeUserUsage | null | undefined) =>
        formatCumulativeUserUsage(
          user,
          (quota) =>
            formatQuotaInCurrency(quota, currency, localized(), config),
          creditLabel,
          displayLocale
        ),
      formatExactQuota: (
        quota: string,
        options?: Pick<
          CurrencyFormatOptions,
          'locale' | 'creditLabel' | 'showSymbol'
        >
      ) =>
        formatExactQuotaInCurrency(quota, currency, localized(options), config),
      formatQuota: (quota: number, options?: CurrencyFormatOptions) =>
        formatQuotaInCurrency(quota, currency, localized(options), config),
      quotaToAmount: (quota: number) =>
        quotaToDisplayAmount(quota, currency, config),
      quotaToInput: (quota: number) =>
        quotaToDisplayInput(quota, currency, config),
      amountToQuota: (amount: number | string) =>
        displayAmountToQuota(amount, currency, config),
      formatUSD: (usd: number, options?: CurrencyFormatOptions) =>
        formatUSDInCurrency(usd, currency, localized(options), config),
      legacyAmountToQuota: (amount: number | string) =>
        legacyPlatformAmountToQuota(amount, config),
      quotaToLegacyAmount: (quota: number) =>
        quotaToLegacyPlatformAmount(quota, config),
      formatLegacyAmount: (amount: number, options?: CurrencyFormatOptions) =>
        formatQuotaInCurrency(
          legacyPlatformAmountToQuota(amount, config),
          currency,
          localized(options),
          config
        ),
    }
  }, [currencyConfig, currency, preference, label, language, creditLabel])
  const saving = Boolean(pending?.key === key && pending.saving)
  const error = pending?.key === key ? pending.error : null
  return useMemo(
    () => ({ ...snapshot, setPreference, saving, error }),
    [snapshot, setPreference, saving, error]
  )
}
