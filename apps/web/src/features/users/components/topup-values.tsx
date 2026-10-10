/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useTranslation } from 'react-i18next'

import { formatRawCreditCount } from '@/lib/cumulative-user-usage'
import {
  formatFiatCurrencyAmount,
  getCurrencyFormattingLocale,
} from '@/lib/currency'

import {
  currentTopupCredits,
  topupPaymentAmounts,
  type TopupRecord,
} from '../lib/topup-display'

type PaymentRecord = TopupRecord & {
  currency?: string
  settlement_currency?: string
  methods?: { settlement_currency?: string }[]
}

function paymentCurrency(record: PaymentRecord): string | null {
  const preferred = (record.currency ?? record.settlement_currency)
    ?.trim()
    .toUpperCase()
  if (preferred) return preferred
  const currencies = new Set(
    record.methods?.map(
      (method) => method.settlement_currency?.trim().toUpperCase() || 'UNKNOWN'
    )
  )
  const known = [...currencies].filter((currency) => currency !== 'UNKNOWN')
  if (known.length > 1) return 'MULTIPLE'
  if (currencies.has('UNKNOWN')) return 'UNKNOWN'
  return currencies.size === 1 ? [...currencies][0] : null
}

export function TopupCreditsValue({ record }: { record?: TopupRecord }) {
  const { t, i18n } = useTranslation()
  const locale = getCurrencyFormattingLocale(
    i18n.resolvedLanguage || i18n.language
  )
  const credits = currentTopupCredits(record)
  if (!record || record.orders === 0 || credits !== null) {
    return (
      <span>{formatRawCreditCount(credits ?? 0, t('Credits'), locale)}</span>
    )
  }
  return (
    <span>
      <span>—</span>
      <span className='text-muted-foreground block text-xs'>
        {t('Historical original points')}:{' '}
        {formatRawCreditCount(record.quota, '', locale).trim()}
      </span>
    </span>
  )
}

export function TopupPaymentValue({ record }: { record?: PaymentRecord }) {
  const { t } = useTranslation()
  const amounts = topupPaymentAmounts(record)
  const currency = record ? paymentCurrency(record) : null
  function amountText(micros: number): string {
    if (currency === 'MULTIPLE') return t('Multiple fiat currencies')
    if (currency && currency !== 'UNKNOWN') {
      return formatFiatCurrencyAmount(micros / 1_000_000, currency)
    }
    if (record?.methods) return t('Currency unavailable')
    const amount = new Intl.NumberFormat(undefined, {
      maximumFractionDigits: 6,
    }).format(micros / 1_000_000)
    return `${amount} (${t('Currency unavailable')})`
  }
  return (
    <span>
      <span>
        {amounts.settled === null ? '—' : amountText(amounts.settled)}
      </span>
      {amounts.historical !== null && (
        <span className='text-muted-foreground block text-xs'>
          {t('Historical order amount')}: {amountText(amounts.historical)}
        </span>
      )}
    </span>
  )
}
