/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

import { isNativeSessionEndpointType } from '../constants'
import { formatModelPrice } from '../lib/price-display'
import { estimateRequestCost } from '../lib/request-estimate'
import type { ModelDetailsContentProps } from './model-details'

/** Common shapes of a single request, so nobody has to guess token counts. */
const PRESETS = [
  { key: 'Short chat', input: '2000', output: '500' },
  { key: 'Long document', input: '50000', output: '4000' },
  { key: 'Code review', input: '20000', output: '8000' },
] as const

const SYSTEMONE_PRESETS = [
  { key: 'Long document', input: '50000', output: '0' },
] as const

export function RequestEstimator(props: ModelDetailsContentProps) {
  const { t } = useTranslation()
  const id = useId()
  const [input, setInput] = useState('10000')
  const [output, setOutput] = useState('2000')
  const [cached, setCached] = useState('0')
  const isSystemone =
    props.model.supported_endpoint_types?.includes('systemone') ?? false
  const endpoints = props.model.supported_endpoint_types
  const isNativeSession = endpoints?.length
    ? endpoints.some(isNativeSessionEndpointType)
    : /(^|\/)gpt-(?:live|realtime)(?:-|$)/.test(props.model.model_name)
  if (isNativeSession) return null
  const presets = isSystemone ? SYSTEMONE_PRESETS : PRESETS
  const parse = (value: string) =>
    value.trim() === '' ? Number.NaN : Number(value)
  const amount = estimateRequestCost(
    props.model,
    1,
    parse(input),
    isSystemone ? 0 : parse(output),
    isSystemone ? 0 : parse(cached)
  )
  return (
    <section
      className='space-y-3 border-t pt-4'
      aria-labelledby={`${id}-title`}
    >
      <h3 id={`${id}-title`} className='text-sm font-semibold'>
        {t('Request cost estimate')}
      </h3>
      <p className='text-muted-foreground text-xs'>{t('Base price (1×)')}</p>
      {props.model.quota_type === 0 && (
        <>
          <div className='flex flex-wrap gap-1.5'>
            {presets.map((preset) => {
              const active =
                input === preset.input &&
                (isSystemone || output === preset.output)
              return (
                <button
                  key={preset.key}
                  type='button'
                  aria-pressed={active}
                  onClick={() => {
                    setInput(preset.input)
                    setOutput(preset.output)
                    setCached('0')
                  }}
                  className={cn(
                    'min-h-9 rounded-md border px-2.5 text-xs font-medium transition-colors',
                    active
                      ? 'border-primary/50 bg-primary/10 text-foreground'
                      : 'border-border/70 text-muted-foreground hover:bg-muted/50 hover:text-foreground'
                  )}
                >
                  {t(preset.key)}
                </button>
              )
            })}
          </div>
          <div className={cn('grid gap-3', !isSystemone && 'sm:grid-cols-3')}>
            {[
              {
                key: 'input',
                label: t('Input Tokens'),
                value: input,
                set: setInput,
              },
              ...(isSystemone
                ? []
                : [
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
                  ]),
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
        </>
      )}
      <p className='text-2xl font-semibold tabular-nums' aria-live='polite'>
        {amount === null
          ? t('Estimate unavailable')
          : formatModelPrice(amount, props.displayCurrency ?? 'USD')}
      </p>
      <details className='group/estimate'>
        <summary className='text-muted-foreground hover:text-foreground cursor-pointer list-none text-xs'>
          {t('What is included in this estimate?')}
        </summary>
        <p className='text-muted-foreground mt-1 text-xs leading-relaxed'>
          {t(
            isSystemone
              ? 'One request at the base price (1×), using input tokens. Actual charges may differ.'
              : 'One request at the base price (1×). Cached tokens are part of input. Excludes cache writes, media and tool fees; actual charges may differ.'
          )}
          {amount === null
            ? ` ${t('Check token counts. Dynamic or special billing requires the pricing rules above.')}`
            : ''}
        </p>
      </details>
    </section>
  )
}
