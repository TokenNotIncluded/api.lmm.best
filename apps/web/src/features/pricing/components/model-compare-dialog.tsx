/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { Crown, X } from 'lucide-react'
import { useId, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  formatLatency,
  formatThroughput,
} from '@/features/performance-metrics/lib/format'
import { formatCompactNumber } from '@/lib/format'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import { DEFAULT_TOKEN_UNIT, getEndpointTypeLabel } from '../constants'
import { parseTags } from '../lib/filters'
import {
  estimateWorkloadCost,
  getCompareTokenPrice,
  getSavingsPercent,
  pickBestIndexes,
} from '../lib/model-compare'
import { getDisplayPriceGroup } from '../lib/model-helpers'
import type { ModelPerfBadgeData } from '../lib/model-perf'
import { formatRequestPrice } from '../lib/price'
import { formatModelPrice } from '../lib/price-display'
import type {
  PriceDisplayCurrency,
  PriceType,
  PricingModel,
  TokenUnit,
} from '../types'
import { ModelBillingModeBadge } from './model-billing-mode-badge'
import { ModelRuntimeBadge } from './model-runtime-badge'

const WORKLOAD_PRESETS = [
  { key: 'Short chat', input: '2000', output: '500' },
  { key: 'Long document', input: '50000', output: '4000' },
  { key: 'Code review', input: '20000', output: '8000' },
] as const

export interface ModelCompareDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  models: PricingModel[]
  tokenUnit?: TokenUnit
  displayCurrency?: PriceDisplayCurrency
  selectedGroup?: string
  perfMap?: ReadonlyMap<string, ModelPerfBadgeData>
  onRemove: (modelName: string) => void
  onViewDetails: (modelName: string) => void
}

type CompareRow = {
  key: string
  label: string
  cells: ReactNode[]
  best?: Set<number>
}

type CompareSection = { key: string; title: string; rows: CompareRow[] }

function parseCount(value: string): number {
  if (value.trim() === '') return Number.NaN
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed >= 0 ? parsed : Number.NaN
}

const muted = (text: string) => (
  <span className='text-muted-foreground'>{text}</span>
)

export function ModelCompareDialog(props: ModelCompareDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const tokenUnit = props.tokenUnit ?? DEFAULT_TOKEN_UNIT
  const currency = props.displayCurrency ?? 'USD'
  const tokenUnitLabel = tokenUnit === 'K' ? '1K' : '1M'
  const [input, setInput] = useState<string>(WORKLOAD_PRESETS[0].input)
  const [output, setOutput] = useState<string>(WORKLOAD_PRESETS[0].output)
  const [requestsPerDay, setRequestsPerDay] = useState('100')

  const costs = useMemo(
    () =>
      props.models.map((model) =>
        estimateWorkloadCost(
          model,
          {
            input: parseCount(input),
            output: parseCount(output),
            requestsPerDay: parseCount(requestsPerDay),
          },
          props.selectedGroup
        )
      ),
    [input, output, props.models, props.selectedGroup, requestsPerDay]
  )
  const monthly = costs.map((cost) => cost?.perMonth ?? null)
  const cheapest = pickBestIndexes(monthly, 'min')
  const cheapestPerRequest = pickBestIndexes(
    costs.map((cost) => cost?.perRequest ?? null),
    'min'
  )
  const savings = getSavingsPercent(monthly)
  const maxMonthly = Math.max(
    0,
    ...monthly.filter((value): value is number => value !== null)
  )
  const winner =
    cheapest.size === 1 ? props.models[[...cheapest][0]]?.model_name : undefined
  const perfs = props.models.map((model) =>
    props.perfMap?.get(model.model_name)
  )

  const tokenPriceRow = (type: PriceType, label: string): CompareRow => {
    const values = props.models.map((model) =>
      getCompareTokenPrice(model, type, tokenUnit, props.selectedGroup)
    )
    return {
      key: type,
      label,
      best: pickBestIndexes(values, 'min'),
      cells: values.map((value) =>
        value === null ? muted('—') : formatModelPrice(value, currency)
      ),
    }
  }

  const pricingRows: CompareRow[] = [
    {
      key: 'billing',
      label: t('Billing'),
      cells: props.models.map((model) => (
        <ModelBillingModeBadge key={model.model_name} model={model} />
      )),
    },
    {
      key: 'group',
      label: t('Group'),
      cells: props.models.map((model) => {
        const group = getDisplayPriceGroup(model, props.selectedGroup)
        return group ? (
          <span key={model.model_name} className='wrap-anywhere'>
            {group}
          </span>
        ) : (
          muted('—')
        )
      }),
    },
    tokenPriceRow('input', t('Input')),
    tokenPriceRow('output', t('Output')),
    tokenPriceRow('cache', t('Cached')),
  ]
  if (props.models.some((model) => model.quota_type === 1)) {
    pricingRows.push({
      key: 'per-request',
      label: t('Per Request'),
      cells: props.models.map((model) =>
        model.quota_type === 1
          ? formatRequestPrice(model, currency, props.selectedGroup)
          : muted('—')
      ),
    })
  }

  const numericRow = (
    key: string,
    label: string,
    values: Array<number | undefined>,
    direction: 'min' | 'max',
    format: (value: number) => string
  ): CompareRow | null => {
    if (!values.some((value) => typeof value === 'number' && value > 0)) {
      return null
    }
    return {
      key,
      label,
      best: pickBestIndexes(
        values.map((value) => (value && value > 0 ? value : null)),
        direction
      ),
      cells: values.map((value) =>
        value && value > 0 ? format(value) : muted('—')
      ),
    }
  }

  const performanceRows = [
    numericRow(
      'latency',
      t('Latency'),
      perfs.map((perf) => perf?.avg_latency_ms),
      'min',
      formatLatency
    ),
    numericRow(
      'throughput',
      t('Throughput'),
      perfs.map((perf) => perf?.avg_tps),
      'max',
      formatThroughput
    ),
    numericRow(
      'success',
      t('Success rate'),
      perfs.map((perf) => perf?.success_rate),
      'max',
      (value) => `${value.toFixed(1)}%`
    ),
  ].filter((row): row is CompareRow => row !== null)

  const capabilityRows = [
    numericRow(
      'context',
      t('Context length'),
      props.models.map((model) => model.context_length),
      'max',
      (value) => formatCompactNumber(value)
    ),
    numericRow(
      'max-output',
      t('Max output tokens'),
      props.models.map((model) => model.max_output_tokens),
      'max',
      (value) => formatCompactNumber(value)
    ),
  ].filter((row): row is CompareRow => row !== null)
  capabilityRows.push(
    {
      key: 'endpoints',
      label: t('Endpoints'),
      cells: props.models.map((model) =>
        model.supported_endpoint_types?.length ? (
          <ChipList
            key={model.model_name}
            items={model.supported_endpoint_types.map((endpoint) =>
              getEndpointTypeLabel(endpoint, t)
            )}
          />
        ) : (
          muted('—')
        )
      ),
    },
    {
      key: 'tags',
      label: t('Tags'),
      cells: props.models.map((model) => {
        const tags = parseTags(model.tags)
        return tags.length ? (
          <ChipList key={model.model_name} items={tags} />
        ) : (
          muted('—')
        )
      }),
    },
    {
      key: 'status',
      label: t('Status'),
      cells: props.models.map((model) => (
        <ModelRuntimeBadge key={model.model_name} state={model.runtime_state} />
      )),
    }
  )

  const sections: CompareSection[] = [
    {
      key: 'pricing',
      title: t('Pricing per {{unit}} tokens', { unit: tokenUnitLabel }),
      rows: pricingRows,
    },
    ...(performanceRows.length > 0
      ? [{ key: 'performance', title: t('Performance'), rows: performanceRows }]
      : []),
    { key: 'capabilities', title: t('Specifications'), rows: capabilityRows },
  ]

  const activePreset = WORKLOAD_PRESETS.find(
    (preset) => preset.input === input && preset.output === output
  )
  const columnTemplate = `minmax(8.5rem, 10rem) repeat(${props.models.length}, minmax(11rem, 1fr))`

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='gap-5 p-4 sm:max-w-6xl sm:p-6'>
        <DialogHeader className='pe-8'>
          <DialogTitle className='font-serif text-2xl font-normal tracking-tight'>
            {t('Compare models')}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Side by side prices, speed and capabilities. Green marks the best value in each row.'
            )}
          </DialogDescription>
        </DialogHeader>

        <section
          aria-labelledby={`${id}-workload`}
          className='bg-muted/30 grid gap-3 rounded-2xl p-3 sm:p-4'
        >
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <h3 id={`${id}-workload`} className='text-sm font-semibold'>
              {t('Your workload')}
            </h3>
            <div className='flex flex-wrap gap-1.5'>
              {WORKLOAD_PRESETS.map((preset) => (
                <button
                  key={preset.key}
                  type='button'
                  aria-pressed={activePreset === preset}
                  onClick={() => {
                    setInput(preset.input)
                    setOutput(preset.output)
                  }}
                  className={cn(
                    'min-h-8 rounded-lg border px-2.5 text-xs font-medium transition-colors motion-reduce:transition-none',
                    activePreset === preset
                      ? 'border-primary/50 bg-primary/10 text-foreground'
                      : 'border-border/70 text-muted-foreground hover:bg-muted/50 hover:text-foreground'
                  )}
                >
                  {t(preset.key)}
                </button>
              ))}
            </div>
          </div>
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
                key: 'rpd',
                label: t('Requests per day'),
                value: requestsPerDay,
                set: setRequestsPerDay,
              },
            ].map((field) => (
              <label
                key={field.key}
                htmlFor={`${id}-${field.key}`}
                className='text-muted-foreground grid gap-1 text-xs'
              >
                {field.label}
                <Input
                  id={`${id}-${field.key}`}
                  type='number'
                  inputMode='numeric'
                  min={0}
                  step={1}
                  value={field.value}
                  onChange={(event) => field.set(event.target.value)}
                  className='bg-background tabular-nums'
                />
              </label>
            ))}
          </div>
          <p
            aria-live='polite'
            className={cn(
              'min-h-5 text-sm',
              winner ? 'text-foreground' : 'text-muted-foreground'
            )}
          >
            {winner && savings !== null ? (
              <>
                <Crown
                  className='text-success me-1.5 inline size-4 -translate-y-px'
                  aria-hidden='true'
                />
                {t(
                  '{{model}} is the cheapest for this workload, about {{percent}}% less than the priciest pick.',
                  { model: winner, percent: Math.round(savings) }
                )}
              </>
            ) : (
              t(
                'Monthly cost assumes 30 days. Media, tools and cache writes are excluded.'
              )
            )}
          </p>
        </section>

        <div className='-mx-4 overflow-x-auto px-4 sm:mx-0 sm:px-0'>
          <div
            role='table'
            aria-label={t('Compare models')}
            className='grid min-w-max text-sm sm:min-w-0'
            style={{ gridTemplateColumns: columnTemplate }}
          >
            <div role='row' className='contents'>
              <div
                role='columnheader'
                className='bg-popover sticky start-0 z-10'
              />
              {props.models.map((model) => (
                <ModelColumnHeader
                  key={model.model_name}
                  model={model}
                  onRemove={() => props.onRemove(model.model_name)}
                  onViewDetails={() => props.onViewDetails(model.model_name)}
                  removable={props.models.length > 2}
                />
              ))}
            </div>

            <div role='row' className='contents'>
              <RowLabel>{t('Cost per request')}</RowLabel>
              {costs.map((cost, index) => (
                <Cell
                  key={props.models[index].model_name}
                  best={cheapestPerRequest.has(index)}
                >
                  {cost
                    ? formatModelPrice(cost.perRequest, currency)
                    : muted(t('Estimate unavailable'))}
                </Cell>
              ))}
            </div>
            <div role='row' className='contents'>
              <RowLabel>{t('Estimated monthly cost')}</RowLabel>
              {monthly.map((value, index) => (
                <Cell
                  key={props.models[index].model_name}
                  best={cheapest.has(index)}
                >
                  {value === null ? (
                    muted('—')
                  ) : (
                    <div className='grid w-full gap-1.5'>
                      <span className='text-base font-semibold'>
                        {formatModelPrice(value, currency, { digitsLarge: 2 })}
                      </span>
                      <span
                        className='bg-muted block h-1.5 w-full overflow-hidden rounded-full'
                        aria-hidden='true'
                      >
                        <span
                          className={cn(
                            'block h-full rounded-full transition-[width] duration-500 ease-out motion-reduce:transition-none',
                            cheapest.has(index)
                              ? 'bg-success'
                              : 'bg-foreground/40'
                          )}
                          style={{
                            width: `${maxMonthly > 0 ? Math.max(3, (value / maxMonthly) * 100) : 3}%`,
                          }}
                        />
                      </span>
                    </div>
                  )}
                </Cell>
              ))}
            </div>

            {sections.map((section) => (
              <SectionRows
                key={section.key}
                title={section.title}
                rows={section.rows}
                columnCount={props.models.length}
                modelNames={props.models.map((model) => model.model_name)}
              />
            ))}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function ModelColumnHeader(props: {
  model: PricingModel
  removable: boolean
  onRemove: () => void
  onViewDetails: () => void
}) {
  const { t } = useTranslation()
  const iconKey = props.model.icon || props.model.vendor_icon
  return (
    <div
      role='columnheader'
      className='border-border/60 flex min-w-0 flex-col gap-2 border-b px-3 pb-3'
    >
      <div className='flex items-start justify-between gap-2'>
        <span className='bg-muted/50 flex size-9 shrink-0 items-center justify-center rounded-xl'>
          {iconKey ? (
            getLobeIcon(iconKey, 22)
          ) : (
            <span className='text-muted-foreground text-sm font-bold'>
              {props.model.model_name.charAt(0).toUpperCase()}
            </span>
          )}
        </span>
        {props.removable && (
          <button
            type='button'
            onClick={props.onRemove}
            className='text-muted-foreground hover:text-foreground hover:bg-muted focus-visible:ring-ring/50 inline-flex size-8 items-center justify-center rounded-lg outline-none focus-visible:ring-2'
            aria-label={`${t('Remove')} ${props.model.model_name}`}
          >
            <X className='size-4' aria-hidden='true' />
          </button>
        )}
      </div>
      <div className='min-w-0'>
        <p className='font-mono text-[13px] leading-5 font-semibold wrap-anywhere'>
          {props.model.model_name}
        </p>
        {props.model.vendor_name && (
          <p className='text-muted-foreground text-xs'>
            {props.model.vendor_name}
          </p>
        )}
      </div>
      <button
        type='button'
        onClick={props.onViewDetails}
        className='text-muted-foreground hover:text-foreground w-fit text-xs underline-offset-4 hover:underline'
      >
        {t('Details')}
      </button>
    </div>
  )
}

function SectionRows(props: {
  title: string
  rows: CompareRow[]
  columnCount: number
  modelNames: string[]
}) {
  return (
    <>
      <div role='row' className='contents'>
        <div
          role='rowheader'
          className='text-muted-foreground bg-popover sticky start-0 z-10 pt-6 pb-2 text-xs font-semibold tracking-wide'
        >
          {props.title}
        </div>
        {Array.from({ length: props.columnCount }, (_, index) => (
          <div key={index} aria-hidden='true' className='pt-6 pb-2' />
        ))}
      </div>
      {props.rows.map((row) => (
        <div key={row.key} role='row' className='contents'>
          <RowLabel>{row.label}</RowLabel>
          {row.cells.map((cell, index) => (
            <Cell key={props.modelNames[index]} best={row.best?.has(index)}>
              {cell}
            </Cell>
          ))}
        </div>
      ))}
    </>
  )
}

function RowLabel(props: { children: ReactNode }) {
  return (
    <div
      role='rowheader'
      className='text-muted-foreground bg-popover border-border/50 sticky start-0 z-10 flex items-center border-b py-2.5 pe-3 text-xs'
    >
      {props.children}
    </div>
  )
}

function Cell(props: { children: ReactNode; best?: boolean }) {
  const { t } = useTranslation()
  return (
    <div
      role='cell'
      className={cn(
        'border-border/50 flex min-w-0 items-center gap-1.5 border-b px-3 py-2.5 font-mono tabular-nums transition-colors',
        props.best && 'bg-success/10 text-success font-semibold'
      )}
    >
      {props.children}
      {props.best && <span className='sr-only'>({t('Best')})</span>}
    </div>
  )
}

function ChipList(props: { items: string[] }) {
  return (
    <span className='flex flex-wrap gap-1 font-sans'>
      {props.items.map((item) => (
        <span
          key={item}
          className='bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-[11px] leading-4'
        >
          {item}
        </span>
      ))}
    </span>
  )
}
