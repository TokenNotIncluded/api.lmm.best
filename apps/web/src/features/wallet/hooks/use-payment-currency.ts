/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { createContext, useContext, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  displayAmountToQuota,
  formatQuotaInCurrency,
  formatUSDInCurrency,
  getCurrencyDisplay,
  getCurrencyFormattingLocale,
  legacyPlatformAmountToQuota,
  quotaToDisplayAmount,
  quotaToDisplayInput,
  quotaToLegacyPlatformAmount,
  resolveWalletDisplayCurrency,
  type CurrencyFormatOptions,
} from '@/lib/currency'
import { useSystemConfigStore } from '@/stores/system-config-store'

export type PaymentDisplayCurrency = 'CNY' | 'USD'
export type PaymentDisplayPreference = '' | PaymentDisplayCurrency
export const PaymentCurrencyContext = createContext<{
  preference: PaymentDisplayPreference
  setPreference: (currency: PaymentDisplayCurrency) => void
} | null>(null)

export function usePaymentCurrency() {
  const context = useContext(PaymentCurrencyContext)
  const [localPreference, setLocalPreference] =
    useState<PaymentDisplayPreference>('')
  const { i18n } = useTranslation()
  const currencyConfig = useSystemConfigStore((state) => state.config.currency)
  const language = i18n.resolvedLanguage || i18n.language
  const preference = context?.preference ?? localPreference
  const automatic = resolveWalletDisplayCurrency('', language)
  const currency: PaymentDisplayCurrency =
    preference === 'CNY' || preference === 'USD'
      ? preference
      : automatic === 'CNY'
        ? 'CNY'
        : 'USD'
  const setPreference = context?.setPreference ?? setLocalPreference

  return useMemo(() => {
    const config = getCurrencyDisplay(currencyConfig).config
    const locale = getCurrencyFormattingLocale(language)
    const localized = (options?: CurrencyFormatOptions) => ({
      ...options,
      locale: options?.locale ?? locale,
    })
    return {
      config,
      currency,
      preference,
      label: currency,
      setPreference,
      step: 'any' as const,
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
  }, [currencyConfig, currency, preference, language, setPreference])
}
