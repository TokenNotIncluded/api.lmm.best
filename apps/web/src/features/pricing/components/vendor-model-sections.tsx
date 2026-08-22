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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { getLobeIcon } from '@/lib/lobe-icon'

import type { PricingModel, TokenUnit } from '../types'
import { ModelCard } from './model-card'
import type { ModelPerfBadgeData } from './model-perf-badge'

export interface VendorModelSectionProps {
  models: PricingModel[]
  onModelClick: (modelName: string) => void
  priceRate?: number
  usdExchangeRate?: number
  tokenUnit?: TokenUnit
  showRechargePrice?: boolean
  selectedGroup?: string
  perfMap?: Map<string, ModelPerfBadgeData>
}

type VendorGroup = {
  name: string
  icon?: string
  models: PricingModel[]
}

/**
 * gpt.ge-style model square body: models grouped by vendor, each group led
 * by a soft translucent header card (vendor mark, name, count) and followed
 * by a responsive card grid.
 */
export function VendorModelSections(props: VendorModelSectionProps) {
  const { t } = useTranslation()

  const groups = useMemo<VendorGroup[]>(() => {
    const byVendor = new Map<string, VendorGroup>()
    for (const model of props.models) {
      const name = model.vendor_name || t('Others')
      let group = byVendor.get(name)
      if (!group) {
        group = { name, icon: model.vendor_icon, models: [] }
        byVendor.set(name, group)
      }
      group.models.push(model)
    }
    // Largest catalog first, then alphabetical — keeps the page stable.
    return [...byVendor.values()].sort(
      (a, b) =>
        b.models.length - a.models.length || a.name.localeCompare(b.name)
    )
  }, [props.models, t])

  if (groups.length === 0) return null

  return (
    <div className='min-w-0 space-y-10'>
      {groups.map((group) => {
        const vendorIcon = group.icon ? getLobeIcon(group.icon, 28) : null
        return (
          <section
            key={group.name}
            aria-labelledby={`vendor-section-${group.name}`}
          >
            <div className='bg-card/20 border-border/40 mb-4 flex min-h-16 gap-3 rounded-xl border p-3 max-md:flex-col md:items-center'>
              <div className='flex flex-1 items-center gap-3'>
                <div className='bg-muted flex size-11 shrink-0 items-center justify-center rounded-xl'>
                  {vendorIcon ?? (
                    <span
                      className='bg-foreground/90 text-primary-foreground flex size-6 items-center justify-center rounded-full text-[0.75em] font-semibold'
                      aria-hidden='true'
                    >
                      {group.name.charAt(0).toUpperCase()}
                    </span>
                  )}
                </div>
                <div className='min-w-0 flex-1'>
                  <div className='flex flex-wrap items-center gap-2'>
                    <h2
                      id={`vendor-section-${group.name}`}
                      className='text-foreground truncate text-base font-semibold'
                    >
                      {group.name}
                    </h2>
                    <span className='text-muted-foreground text-xs'>
                      {t('{{count}} models', { count: group.models.length })}
                    </span>
                  </div>
                </div>
              </div>
            </div>

            <div className='grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4'>
              {group.models.map((model) => (
                <ModelCard
                  key={model.id ?? model.model_name}
                  model={model}
                  tokenUnit={props.tokenUnit}
                  priceRate={props.priceRate}
                  usdExchangeRate={props.usdExchangeRate}
                  showRechargePrice={props.showRechargePrice}
                  selectedGroup={props.selectedGroup}
                  perf={props.perfMap?.get(model.model_name || '')}
                  onClick={() => props.onModelClick(model.model_name || '')}
                />
              ))}
            </div>
          </section>
        )
      })}
    </div>
  )
}
