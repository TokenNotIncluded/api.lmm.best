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
import { ChevronRight, Copy } from 'lucide-react'
import { memo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import { DEFAULT_TOKEN_UNIT, getEndpointTypeLabel } from '../constants'
import {
  getDynamicDisplayGroupRatio,
  getDynamicPricingSummary,
} from '../lib/dynamic-price'
import { parseTags } from '../lib/filters'
import { getDisplayPriceGroup, isTokenBasedModel } from '../lib/model-helpers'
import type { ModelPerfBadgeData } from '../lib/model-perf'
import { formatPrice, formatRequestPrice } from '../lib/price'
import type { PricingModel, TokenUnit, PriceDisplayCurrency } from '../types'
import { ModelBillingModeBadge } from './model-billing-mode-badge'
import { ModelPerfBadge } from './model-perf-badge'
import { ModelRuntimeBadge } from './model-runtime-badge'

export interface ModelCardProps {
  model: PricingModel
  onClick: () => void
  tokenUnit?: TokenUnit
  displayCurrency?: PriceDisplayCurrency
  selectedGroup?: string
  perf?: ModelPerfBadgeData
}

export const ModelCard = memo(function ModelCard(props: ModelCardProps) {
  const { t } = useTranslation()
  const { copyToClipboard } = useCopyToClipboard()
  const tokenUnit = props.tokenUnit ?? DEFAULT_TOKEN_UNIT
  const displayCurrency = props.displayCurrency ?? 'USD'
  const isTokenBased = isTokenBasedModel(props.model)
  const tokenUnitLabel = tokenUnit === 'K' ? '1K' : '1M'
  const tags = parseTags(props.model.tags)
  const groups = props.model.enable_groups || []
  const endpoints = props.model.supported_endpoint_types || []
  const modelIconKey = props.model.icon || props.model.vendor_icon
  const modelIcon = modelIconKey ? getLobeIcon(modelIconKey, 28) : null
  const initial = props.model.model_name?.charAt(0).toUpperCase() || '?'
  const isDynamicPricing =
    props.model.billing_mode === 'tiered_expr' &&
    Boolean(props.model.billing_expr)
  const hasCachedPrice = isTokenBased && props.model.cache_read_price != null
  const dynamicSummary = isDynamicPricing
    ? getDynamicPricingSummary(props.model, {
        tokenUnit,
        displayCurrency,
        groupRatioMultiplier: getDynamicDisplayGroupRatio(
          props.model,
          props.selectedGroup
        ),
      })
    : null
  const hasDurationPrice = dynamicSummary?.entries.some(
    (entry) => entry.unit === 'minute'
  )

  const primaryGroup = getDisplayPriceGroup(props.model, props.selectedGroup)
  const bottomTags = [
    ...endpoints
      .slice(0, 2)
      .map((endpoint) => getEndpointTypeLabel(endpoint, t)),
    ...tags.slice(0, 2),
  ]
  const hiddenCount =
    Math.max(groups.length - 1, 0) +
    Math.max(endpoints.length - 2, 0) +
    Math.max(tags.length - 2, 0)

  const handleCopy = (e: React.MouseEvent) => {
    e.stopPropagation()
    copyToClipboard(props.model.model_name || '')
  }

  let priceSummary: ReactNode
  if (dynamicSummary) {
    if (dynamicSummary.isSpecialExpression) {
      priceSummary = (
        <span className='min-w-0'>
          <span className='forge-price-warning-text'>
            {t('Special billing expression')}
          </span>
          <code className='text-muted-foreground mt-0.5 line-clamp-1 block font-mono text-xs break-all'>
            {dynamicSummary.rawExpression}
          </code>
        </span>
      )
    } else if (dynamicSummary.primaryEntries.length > 0) {
      priceSummary = (
        <>
          {dynamicSummary.primaryEntries.map((entry) => (
            <span
              key={entry.key}
              className='text-muted-foreground whitespace-nowrap'
            >
              {t(entry.shortLabel)}{' '}
              <span className='text-foreground font-mono font-semibold'>
                {entry.formatted}
                {hasDurationPrice && (
                  <span className='text-muted-foreground ml-1 text-xs font-normal'>
                    / {entry.unit === 'minute' ? t('minute') : tokenUnitLabel}
                  </span>
                )}
              </span>
            </span>
          ))}
        </>
      )
    } else {
      priceSummary = (
        <span className='text-muted-foreground text-sm'>
          {t('Tiered pricing')}
        </span>
      )
    }
  } else if (isTokenBased) {
    priceSummary = (
      <>
        <span className='text-muted-foreground whitespace-nowrap'>
          {t('Input')}{' '}
          <span className='text-foreground font-mono font-semibold'>
            {formatPrice(
              props.model,
              'input',
              tokenUnit,
              displayCurrency,
              props.selectedGroup
            )}
          </span>
        </span>
        <span className='text-muted-foreground whitespace-nowrap'>
          {t('Output')}{' '}
          <span className='text-foreground font-mono font-semibold'>
            {formatPrice(
              props.model,
              'output',
              tokenUnit,
              displayCurrency,
              props.selectedGroup
            )}
          </span>
        </span>
        {hasCachedPrice && (
          <span className='text-muted-foreground whitespace-nowrap'>
            {t('Cached')}{' '}
            <span className='text-foreground font-mono font-semibold'>
              {formatPrice(
                props.model,
                'cache',
                tokenUnit,
                displayCurrency,
                props.selectedGroup
              )}
            </span>
          </span>
        )}
      </>
    )
  } else {
    priceSummary = (
      <span className='text-muted-foreground whitespace-nowrap'>
        <span className='text-foreground font-mono font-semibold'>
          {formatRequestPrice(
            props.model,
            displayCurrency,
            props.selectedGroup
          )}
        </span>{' '}
        / {t('request')}
      </span>
    )
  }

  return (
    <div
      className={cn(
        'group relative flex min-w-0 flex-col rounded-xl border p-4 transition-colors sm:p-5 motion-reduce:transition-none',
        'hover:bg-muted/20'
      )}
    >
      {/* Keep the full model identifier clear of the action buttons. */}
      <div className='flex flex-col gap-2.5 sm:gap-3'>
        <div className='flex w-full min-w-0 items-start gap-2.5 sm:gap-3'>
          <div
            className='bg-muted/40 flex size-9 shrink-0 items-center justify-center rounded-lg sm:size-10 sm:rounded-xl'
            aria-hidden='true'
          >
            {modelIcon || (
              <span className='text-muted-foreground text-sm font-bold'>
                {initial}
              </span>
            )}
          </div>
          <div className='min-w-0 flex-1'>
            <h3 className='text-foreground font-mono text-[15px] leading-6 font-semibold wrap-anywhere whitespace-normal'>
              {props.model.model_name}
            </h3>
          </div>
        </div>
      </div>

      <div className='mt-3 flex min-w-0 flex-wrap items-baseline gap-x-4 gap-y-1 border-y py-3 text-sm tabular-nums'>
        {priceSummary}
      </div>

      <p className='text-muted-foreground mt-3 line-clamp-2 min-h-10 flex-1 text-[13px] leading-5'>
        {props.model.description || t('No description available.')}
      </p>

      <div className='mt-4 space-y-2'>
        <div className='flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1'>
          {primaryGroup && (
            <span className='text-muted-foreground text-sm font-medium wrap-anywhere'>
              {primaryGroup}
            </span>
          )}
          <ModelBillingModeBadge model={props.model} />
          <ModelRuntimeBadge state={props.model.runtime_state} />
        </div>
        <div className='flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1'>
          {bottomTags.map((item) => (
            <span
              key={item}
              className='text-muted-foreground text-xs wrap-anywhere'
            >
              {item}
            </span>
          ))}
          {!hasDurationPrice && (
            <span className='text-muted-foreground text-xs'>
              {tokenUnitLabel}
            </span>
          )}
          {hiddenCount > 0 && (
            <span className='text-muted-foreground text-xs'>
              +{hiddenCount}
            </span>
          )}
        </div>
        <ModelPerfBadge perf={props.perf} className='border-t pt-3' />
      </div>

      <div className='mt-4 flex items-center justify-end gap-2'>
        <button
          type='button'
          onClick={handleCopy}
          className='text-muted-foreground hover:text-foreground hover:bg-muted focus-visible:ring-ring/50 inline-flex min-h-11 min-w-11 items-center justify-center rounded-md border p-0 transition-colors outline-none focus-visible:ring-2 motion-reduce:transition-none sm:min-h-8 sm:min-w-8'
          title={t('Copy')}
          aria-label={`${t('Copy')} ${props.model.model_name}`}
        >
          <Copy className='size-3.5' aria-hidden='true' />
        </button>
        <button
          type='button'
          onClick={props.onClick}
          className='text-foreground hover:bg-muted focus-visible:ring-ring/50 inline-flex min-h-11 items-center gap-1 rounded-md border px-3 py-2.5 text-xs font-medium transition-colors outline-none focus-visible:ring-2 motion-reduce:transition-none sm:min-h-8 sm:py-1.5'
        >
          {t('Details')}
          <ChevronRight className='size-3.5' aria-hidden='true' />
        </button>
      </div>
    </div>
  )
})
