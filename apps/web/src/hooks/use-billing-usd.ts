/*
Copyright (C) 2026 LIghtJUNction
*/
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  formatQuotaInCurrency,
  formatUSDInCurrency,
  getCurrencyDisplay,
  getCurrencyFormattingLocale,
  type CurrencyFormatOptions,
} from '@/lib/currency'
import { useSystemConfigStore } from '@/stores/system-config-store'

/** Bills and audit amounts always use USD, independent of wallet preferences. */
export function useBillingUSD() {
  const { i18n } = useTranslation()
  const currencyConfig = useSystemConfigStore((state) => state.config.currency)
  const language = i18n.resolvedLanguage || i18n.language

  return useMemo(() => {
    const config = getCurrencyDisplay(currencyConfig).config
    const locale = getCurrencyFormattingLocale(language)
    const localized = (options?: CurrencyFormatOptions) => ({
      ...options,
      locale: options?.locale ?? locale,
    })
    return {
      currency: 'USD' as const,
      config,
      formatQuota: (quota: number, options?: CurrencyFormatOptions) =>
        formatQuotaInCurrency(quota, 'USD', localized(options), config),
      formatUSD: (amount: number, options?: CurrencyFormatOptions) =>
        formatUSDInCurrency(amount, 'USD', localized(options), config),
    }
  }, [currencyConfig, language])
}
