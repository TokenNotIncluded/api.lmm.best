/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { Columns3, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import { MAX_COMPARE_MODELS } from '../lib/model-compare'
import type { PricingModel } from '../types'

export interface ModelCompareTrayProps {
  models: PricingModel[]
  onRemove: (modelName: string) => void
  onClear: () => void
  onCompare: () => void
}

export function ModelCompareTray(props: ModelCompareTrayProps) {
  const { t } = useTranslation()
  if (props.models.length === 0) return null
  const canCompare = props.models.length >= 2
  const emptySlots = MAX_COMPARE_MODELS - props.models.length

  return (
    <div
      role='region'
      aria-label={t('Model comparison tray')}
      className='pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center px-3 pb-[env(safe-area-inset-bottom)]'
    >
      <div className='bg-popover/95 text-popover-foreground ring-foreground/10 animate-in fade-in-0 slide-in-from-bottom-4 pointer-events-auto flex max-w-full items-center gap-2 rounded-2xl p-2 shadow-2xl ring-1 backdrop-blur-md duration-200 motion-reduce:animate-none sm:gap-3 sm:p-2.5'>
        <ul className='flex min-w-0 items-center gap-1.5 overflow-x-auto'>
          {props.models.map((model) => {
            const iconKey = model.icon || model.vendor_icon
            return (
              <li
                key={model.model_name}
                className='bg-muted/60 animate-in zoom-in-95 fade-in-0 flex h-9 max-w-48 shrink-0 items-center gap-1.5 rounded-xl ps-2 pe-1 duration-150 motion-reduce:animate-none'
              >
                <span className='flex size-5 shrink-0 items-center justify-center'>
                  {iconKey ? (
                    getLobeIcon(iconKey, 18)
                  ) : (
                    <span className='text-muted-foreground text-xs font-bold'>
                      {model.model_name.charAt(0).toUpperCase()}
                    </span>
                  )}
                </span>
                <span className='truncate font-mono text-xs font-medium'>
                  {model.model_name}
                </span>
                <button
                  type='button'
                  onClick={() => props.onRemove(model.model_name)}
                  className='text-muted-foreground hover:text-foreground hover:bg-background focus-visible:ring-ring/50 inline-flex size-7 shrink-0 items-center justify-center rounded-lg outline-none focus-visible:ring-2'
                  aria-label={`${t('Remove')} ${model.model_name}`}
                >
                  <X className='size-3.5' aria-hidden='true' />
                </button>
              </li>
            )
          })}
          {Array.from({ length: emptySlots }, (_, index) => (
            <li
              key={`slot-${index}`}
              aria-hidden='true'
              className='border-foreground/15 hidden h-9 w-20 shrink-0 rounded-xl border border-dashed sm:block'
            />
          ))}
        </ul>
        <div className='flex shrink-0 items-center gap-1'>
          <button
            type='button'
            onClick={props.onClear}
            className='text-muted-foreground hover:text-foreground focus-visible:ring-ring/50 hidden h-9 rounded-xl px-3 text-xs font-medium outline-none focus-visible:ring-2 sm:inline-flex sm:items-center'
          >
            {t('Clear')}
          </button>
          <button
            type='button'
            onClick={props.onCompare}
            disabled={!canCompare}
            title={canCompare ? undefined : t('Pick at least two models')}
            className={cn(
              'bg-primary text-primary-foreground focus-visible:ring-ring/50 inline-flex h-9 items-center gap-1.5 rounded-xl px-3.5 text-xs font-semibold outline-none focus-visible:ring-2 transition-opacity motion-reduce:transition-none',
              !canCompare && 'cursor-not-allowed opacity-50'
            )}
          >
            <Columns3 className='size-4' aria-hidden='true' />
            <span>{t('Compare')}</span>
            <span className='tabular-nums opacity-80'>
              {props.models.length}/{MAX_COMPARE_MODELS}
            </span>
          </button>
        </div>
      </div>
    </div>
  )
}
