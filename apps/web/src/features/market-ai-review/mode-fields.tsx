/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Label } from '@/components/ui/label'

import { MARKET_AI_REVIEW_COPY as copy } from './copy'

export type MarketAIReviewMode = 'off' | 'assist' | 'auto'
export interface MarketAIReviewModes {
  tool: MarketAIReviewMode
  product: MarketAIReviewMode
}

export function MarketAIReviewModeFields({
  value,
  onChange,
  disabled = false,
}: {
  value: MarketAIReviewModes
  onChange: (value: MarketAIReviewModes) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  return (
    <div className='space-y-5'>
      <p className='text-muted-foreground text-sm'>{t(copy.scope)}</p>
      <div className='grid gap-5 lg:grid-cols-2'>
        {(['tool', 'product'] as const).map((market) => (
          <div key={market} className='space-y-2'>
            <Label htmlFor={`market-ai-mode-${market}`}>
              {t(market === 'tool' ? copy.toolMode : copy.productMode)}
            </Label>
            <select
              id={`market-ai-mode-${market}`}
              value={value[market]}
              disabled={disabled}
              onChange={(event) => {
                const next = event.target.value
                if (next === 'off' || next === 'assist' || next === 'auto') {
                  onChange({ ...value, [market]: next })
                }
              }}
              className='border-input bg-background focus-visible:ring-ring/50 min-h-11 w-full rounded-md border px-3 text-base outline-none focus-visible:ring-2 disabled:opacity-50 sm:text-sm'
            >
              <option value='off'>{t(copy.off)}</option>
              <option value='assist'>{t(copy.assist)}</option>
              <option value='auto'>{t(copy.auto)}</option>
            </select>
          </div>
        ))}
      </div>
      <p className='text-muted-foreground text-xs'>{t(copy.limits)}</p>
    </div>
  )
}
