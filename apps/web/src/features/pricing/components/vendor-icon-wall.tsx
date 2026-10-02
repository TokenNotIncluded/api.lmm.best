/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import { FILTER_ALL } from '../constants'
import type { PricingModel, PricingVendor } from '../types'

export interface VendorIconWallProps {
  vendors: PricingVendor[]
  models: PricingModel[]
  activeVendor: string
  onVendorChange: (value: string) => void
}

/**
 * A flat wall of vendor marks above the catalog. Each tile is one click to
 * narrow the whole page to that vendor — the fastest way to answer "who serves
 * this model family", without opening the filter drawer.
 */
export function VendorIconWall(props: VendorIconWallProps) {
  const { t } = useTranslation()
  const counted = props.vendors
    .map((vendor) => ({
      ...vendor,
      modelCount: props.models.reduce(
        (total, model) => total + (model.vendor_name === vendor.name ? 1 : 0),
        0
      ),
    }))
    .filter((vendor) => vendor.modelCount > 0)
    .sort((a, b) => b.modelCount - a.modelCount)

  if (counted.length === 0) {
    return null
  }

  const isAll = props.activeVendor === FILTER_ALL || !props.activeVendor

  return (
    <section aria-label={t('Browse by vendor')} className='mb-6 sm:mb-8'>
      <h2 className='text-foreground mb-2 text-sm font-semibold sm:mb-3'>
        {t('Browse by vendor')}
      </h2>
      <ul className='-mx-5 flex flex-nowrap gap-2 overflow-x-auto px-5 pt-1 pb-2 sm:mx-0 sm:flex-wrap sm:overflow-visible sm:px-0 sm:pt-0 sm:pb-0'>
        <li className='shrink-0'>
          <button
            type='button'
            onClick={() => props.onVendorChange(FILTER_ALL)}
            aria-pressed={isAll}
            className={cn(
              'focus-visible:ring-ring/50 inline-flex min-h-11 items-center gap-2 rounded-lg border px-3 text-sm whitespace-nowrap transition-colors outline-none focus-visible:ring-2 motion-reduce:transition-none',
              isAll
                ? 'border-primary/50 bg-primary/10 text-foreground'
                : 'border-border/70 text-muted-foreground hover:border-border hover:bg-muted/50 hover:text-foreground'
            )}
          >
            {t('All Vendors')}
          </button>
        </li>
        {counted.map((vendor) => {
          const active = props.activeVendor === vendor.name
          return (
            <li key={vendor.id ?? vendor.name} className='shrink-0'>
              <button
                type='button'
                onClick={() =>
                  props.onVendorChange(active ? FILTER_ALL : vendor.name)
                }
                aria-pressed={active}
                title={t('{{count}} models', { count: vendor.modelCount })}
                className={cn(
                  'group focus-visible:ring-ring/50 inline-flex min-h-11 items-center gap-2 rounded-lg border px-3 text-sm whitespace-nowrap transition-colors outline-none focus-visible:ring-2 motion-reduce:transition-none',
                  active
                    ? 'border-primary/50 bg-primary/10 text-foreground'
                    : 'border-border/70 text-muted-foreground hover:border-border hover:bg-muted/50 hover:text-foreground'
                )}
              >
                <span className='shrink-0' aria-hidden='true'>
                  {vendor.icon ? getLobeIcon(vendor.icon, 18) : null}
                </span>
                <span className='max-w-[9rem] truncate'>{vendor.name}</span>
                <span className='text-muted-foreground text-xs tabular-nums'>
                  {vendor.modelCount}
                </span>
              </button>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
