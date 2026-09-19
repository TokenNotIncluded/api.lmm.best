/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'

import { getAvailableGroups } from '../lib/model-helpers'
import { formatModelPrice } from '../lib/price-display'
import { estimateRequestCost } from '../lib/request-estimate'
import type { ModelDetailsContentProps } from './model-details'

export function RequestEstimator(props: ModelDetailsContentProps) {
  const { t } = useTranslation()
  const id = useId()
  const [selected, setSelected] = useState('')
  const [input, setInput] = useState('10000')
  const [output, setOutput] = useState('2000')
  const [cached, setCached] = useState('0')
  const groups = getAvailableGroups(props.model, props.usableGroup)
  const group = groups.includes(selected) ? selected : (groups[0] ?? '')
  const parse = (value: string) =>
    value.trim() === '' ? Number.NaN : Number(value)
  const amount = estimateRequestCost(
    props.model,
    props.groupRatio[group],
    parse(input),
    parse(output),
    parse(cached)
  )
  return (
    <section
      className='space-y-3 border-t pt-4'
      aria-labelledby={`${id}-title`}
    >
      <h3 id={`${id}-title`} className='text-sm font-semibold'>
        {t('Request cost estimate')}
      </h3>
      <label className='grid gap-1 text-sm' htmlFor={`${id}-group`}>
        {t('Group')}
        <select
          id={`${id}-group`}
          className='bg-background h-9 rounded-md border px-2'
          value={group}
          onChange={(event) => setSelected(event.target.value)}
        >
          {groups.length === 0 && (
            <option value=''>{t('No available groups')}</option>
          )}
          {groups.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
      </label>
      {props.model.quota_type === 0 && (
        <div className='grid gap-3 sm:grid-cols-3'>
          {[
            {
              key: 'input',
              label: t('Input Tokens'),
              value: input,
              set: setInput,
            },
            {
              key: 'output',
              label: t('Output Tokens'),
              value: output,
              set: setOutput,
            },
            {
              key: 'cached',
              label: t('Cached input tokens'),
              value: cached,
              set: setCached,
            },
          ].map((field) => (
            <label
              key={field.key}
              className='grid gap-1 text-sm'
              htmlFor={`${id}-${field.key}`}
            >
              {field.label}
              <Input
                id={`${id}-${field.key}`}
                type='number'
                min={0}
                step={1}
                value={field.value}
                onChange={(event) => field.set(event.target.value)}
              />
            </label>
          ))}
        </div>
      )}
      <p className='text-lg font-semibold tabular-nums' aria-live='polite'>
        {amount === null
          ? t('Estimate unavailable')
          : formatModelPrice(
              amount,
              props.showRechargePrice ?? false,
              props.priceRate
            )}
      </p>
      <p className='text-muted-foreground text-xs'>
        {t(
          'One request, selected group multiplier included. Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.'
        )}
      </p>
      {amount === null && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Check token counts and group prices. Dynamic or special billing requires the pricing rules above.'
          )}
        </p>
      )}
    </section>
  )
}
