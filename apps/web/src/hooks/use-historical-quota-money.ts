/*
Copyright (C) 2026 LIghtJUNction
*/
import { useTranslation } from 'react-i18next'

import { getCurrencyFormattingLocale } from '@/lib/currency'
import {
  formatHistoricalRawQuota,
  historicalLogQuotaUSD,
  historicalQuotaUSD,
  type HistoricalQuotaProjection,
} from '@/lib/historical-quota-money'

import { useBillingUSD } from './use-billing-usd'

export function useHistoricalQuotaMoney() {
  const { t, i18n } = useTranslation()
  const { formatUSD } = useBillingUSD()
  const locale = getCurrencyFormattingLocale(
    i18n.resolvedLanguage || i18n.language
  )
  const formatRawQuota = (quota: number) =>
    formatHistoricalRawQuota(quota, t('Raw credits'), locale)
  const formatUSDValue = (usd: number) =>
    formatUSD(usd, { digitsLarge: 4, digitsSmall: 8, abbreviate: false })
  return {
    formatRawQuota,
    formatHistoricalQuota: (
      quota: number,
      projection: HistoricalQuotaProjection | undefined
    ) => {
      const usd = historicalQuotaUSD(projection)
      return usd === null ? formatRawQuota(quota) : formatUSDValue(usd)
    },
    formatHistoricalLogQuota: (
      input: Parameters<typeof historicalLogQuotaUSD>[0]
    ) => {
      const usd = historicalLogQuotaUSD(input)
      return usd === null
        ? formatRawQuota(input.consumedQuota ?? input.quota)
        : formatUSDValue(usd)
    },
  }
}
