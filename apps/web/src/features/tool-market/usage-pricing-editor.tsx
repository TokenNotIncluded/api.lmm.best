/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'

import { marketQuota, type BillingRule } from './api'
import { usageMetrics, maximumUsageQuota } from './usage-pricing'

export function UsagePricingEditor({
  rules,
  metrics,
  disabled,
  onChange,
}: {
  rules: BillingRule[]
  metrics: string[]
  disabled: boolean
  onChange: (rules: BillingRule[]) => void
}) {
  const { t } = useTranslation()
  const { quotaToInput, amountToQuota, formatQuota, label, currency } =
    useWalletCurrency()
  const inputCurrencyKey = `${currency}:${quotaToInput(1)}`
  const [rateDrafts, setRateDrafts] = useState<
    Record<string, { key: string; input: string; quota: number }>
  >({})
  const clearRateDraft = (metric: string) =>
    setRateDrafts((current) => {
      const next = { ...current }
      delete next[metric]
      return next
    })
  const prefix = useId()
  const available = usageMetrics.filter((m) => metrics.includes(m.metric))
  const remaining = available.filter(
    (m) => !rules.some((r) => r.metric === m.metric)
  )
  const update = (index: number, patch: Partial<BillingRule>) =>
    onChange(rules.map((r, i) => (i === index ? { ...r, ...patch } : r)))
  let cap: number | undefined
  try {
    cap = maximumUsageQuota(rules)
  } catch {
    /* incomplete input */
  }
  return (
    <div className='col-span-full space-y-3'>
      <p className='text-muted-foreground text-sm'>
        {t('Usage is reported by the tool provider. Rates are added together.')}
      </p>
      {rules.map((r, index) => {
        const m = usageMetrics.find((item) => item.metric === r.metric)
        return (
          <div key={index} className='grid gap-3 border-b pb-3 sm:grid-cols-3'>
            <Field>
              <FieldLabel htmlFor={`${prefix}-metric-${index}`}>
                {t('Usage unit')}
              </FieldLabel>
              <select
                id={`${prefix}-metric-${index}`}
                className='border-input bg-background min-h-11 rounded-md border px-3 text-base'
                value={r.metric}
                disabled={disabled}
                onChange={(e) => {
                  const next = available.find(
                    (m) => m.metric === e.target.value
                  )
                  if (next) {
                    clearRateDraft(r.metric)
                    clearRateDraft(next.metric)
                    update(index, {
                      metric: next.metric,
                      max_quantity: next.scale,
                    })
                  }
                }}
              >
                {available
                  .filter((m) => m.metric === r.metric || remaining.includes(m))
                  .map((m) => (
                    <option key={m.metric} value={m.metric}>
                      {t(m.label)}
                    </option>
                  ))}
              </select>
            </Field>
            <Field>
              <FieldLabel htmlFor={`${prefix}-rate-${index}`}>
                {t('Price per unit')} ({label})
              </FieldLabel>
              <Input
                id={`${prefix}-rate-${index}`}
                type='text'
                inputMode='decimal'
                value={
                  rateDrafts[r.metric]?.key === inputCurrencyKey &&
                  rateDrafts[r.metric]?.quota === r.rate_quota
                    ? rateDrafts[r.metric].input
                    : r.rate_quota
                      ? quotaToInput(r.rate_quota)
                      : ''
                }
                aria-invalid={
                  !Number.isSafeInteger(r.rate_quota) || r.rate_quota <= 0
                }
                disabled={disabled}
                onChange={(e) => {
                  const input = e.target.value
                  let rate = 0
                  try {
                    rate = marketQuota(input, amountToQuota)
                  } catch {
                    // Incomplete or invalid text cannot submit the previous valid rate.
                  }
                  setRateDrafts((current) => ({
                    ...current,
                    [r.metric]: { input, key: inputCurrencyKey, quota: rate },
                  }))
                  update(index, { rate_quota: rate })
                }}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor={`${prefix}-quantity-${index}`}>
                {t('Maximum per call')}: {t(m?.quantity ?? r.metric)}
              </FieldLabel>
              <Input
                id={`${prefix}-quantity-${index}`}
                type='number'
                min='0'
                step='any'
                value={r.max_quantity / (m?.limitScale ?? 1) || ''}
                disabled={disabled}
                onChange={(e) =>
                  update(index, {
                    max_quantity: Math.round(
                      Number(e.target.value) * (m?.limitScale ?? 1)
                    ),
                  })
                }
              />
            </Field>
            <Button
              type='button'
              variant='outline'
              disabled={disabled}
              onClick={() => {
                clearRateDraft(r.metric)
                onChange(rules.filter((_, i) => i !== index))
              }}
            >
              {t('Remove usage unit')}
            </Button>
          </div>
        )
      })}
      <Button
        type='button'
        variant='outline'
        disabled={disabled || !remaining.length}
        onClick={() => {
          const m = remaining[0]
          if (m) {
            onChange([
              ...rules,
              { metric: m.metric, rate_quota: 0, max_quantity: m.scale },
            ])
          }
        }}
      >
        {t('Add usage unit')}
      </Button>
      <p className={cap === undefined ? 'text-destructive text-sm' : 'text-sm'}>
        {cap === undefined
          ? t('Set a positive rate and maximum for every usage unit.')
          : t(
              'Reserve up to {{amount}}; settle reported usage and release the remainder.',
              { amount: formatQuota(cap) }
            )}
      </p>
    </div>
  )
}
