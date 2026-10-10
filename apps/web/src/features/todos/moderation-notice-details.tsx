/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'

import type { TodoItem } from './api'
import { todoDetailNumber, todoDetailString } from './todo-navigation'

export function ModerationNoticeDetails({ item }: { item: TodoItem }) {
  const { t, i18n } = useTranslation()
  if (item.category !== 'moderation') return null
  const requestId = todoDetailString(item, 'request_id')
  const mode = todoDetailString(item, 'mode')
  const source = todoDetailString(item, 'source')
  const chargedQuota = todoDetailNumber(item, 'charged_quota')
  const requestedQuota = todoDetailNumber(item, 'requested_quota')
  const feeRecordId = todoDetailNumber(item, 'fee_record_id')
  const categories = Array.isArray(item.details?.categories)
    ? item.details.categories.filter(
        (category): category is string =>
          typeof category === 'string' && category.trim().length > 0
      )
    : []
  const formatFee = (quota: number) =>
    formatQuotaWithCurrency(quota, {
      digitsLarge: 6,
      digitsSmall: 6,
      abbreviate: false,
      locale: toIntlLocale(i18n.language),
    })

  return (
    <span className='text-muted-foreground mt-2 grid gap-1 text-xs leading-5'>
      {source === 'relay_input' ||
      source === 'assistant_input' ||
      source === 'assistant_output' ? (
        <span>
          {t('Source')}:{' '}
          {t(
            source === 'relay_input'
              ? 'API user input'
              : source === 'assistant_input'
                ? 'Assistant user input'
                : 'Assistant output'
          )}
        </span>
      ) : null}
      {mode === 'strict' || mode === 'tolerant' ? (
        <span>{t(mode === 'strict' ? 'Strict mode' : 'Tolerant mode')}</span>
      ) : null}
      {categories.length > 0 ? (
        <span className='[overflow-wrap:anywhere]'>
          {t('Categories')}: {categories.join(', ')}
        </span>
      ) : null}
      {requestId ? (
        <span className='[overflow-wrap:anywhere]'>
          {t('Request ID')}: <span className='font-mono'>{requestId}</span>
        </span>
      ) : null}
      {chargedQuota !== undefined && chargedQuota >= 0 ? (
        <span>
          {t('Wallet deduction')}: {formatFee(chargedQuota)}
        </span>
      ) : null}
      {requestedQuota !== undefined &&
      requestedQuota > (chargedQuota ?? requestedQuota) ? (
        <span>
          {t('Requested category fee')}: {formatFee(requestedQuota)}
        </span>
      ) : null}
      {feeRecordId !== undefined &&
      Number.isSafeInteger(feeRecordId) &&
      feeRecordId > 0 ? (
        <span>
          {t('Fee record ID')}: {feeRecordId}
        </span>
      ) : null}
      {source === 'assistant_output' ? (
        <span>
          {t(
            'This warning concerns assistant output; your wallet and risk score are unchanged.'
          )}
        </span>
      ) : null}
    </span>
  )
}
