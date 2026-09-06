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
*/
import { Link } from '@tanstack/react-router'
import { Box, ExternalLink, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { useModelPlaza } from '@/context/model-plaza-provider'
import { usePerfMap } from '@/features/pricing/hooks/use-perf-map'
import { usePricingData } from '@/features/pricing/hooks/use-pricing-data'
import { getModelPerfDisplay } from '@/features/pricing/lib/model-perf'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import type { PricingModel } from '../types'
import { ModelDetailsDrawer } from './model-details'

type AvailabilityFilter = 'all' | 'available' | 'no-data'

function ModelRow({
  model,
  onSelect,
  onClose,
  perf,
  perfLoading,
}: {
  model: PricingModel
  onSelect: () => void
  onClose: () => void
  perf: ReturnType<typeof getModelPerfDisplay> | undefined
  perfLoading: boolean
}) {
  const { t } = useTranslation()
  const iconKey = model.icon || model.vendor_icon
  const icon = iconKey ? getLobeIcon(iconKey, 24) : null
  const initial = model.model_name.charAt(0).toUpperCase() || '?'
  const hasPerformance = Boolean(perf)

  return (
    <div className='border-border/70 hover:bg-muted/30 flex items-center gap-3 border-b px-4 py-3 transition-colors last:border-b-0'>
      <button
        type='button'
        onClick={onSelect}
        className='focus-visible:ring-ring flex min-w-0 flex-1 items-center gap-3 text-left focus-visible:ring-2 focus-visible:outline-none'
      >
        <span className='bg-muted/60 flex size-8 shrink-0 items-center justify-center rounded-md'>
          {icon || (
            <span className='text-muted-foreground text-xs font-bold'>
              {initial}
            </span>
          )}
        </span>
        <span className='min-w-0 flex-1'>
          <span className='text-foreground block truncate text-sm font-semibold'>
            {model.model_name}
          </span>
          <span className='text-muted-foreground block truncate text-xs'>
            {model.vendor_name || t('Others')}
          </span>
        </span>
      </button>

      <span
        className={cn(
          'shrink-0 text-right font-mono text-xs tabular-nums',
          hasPerformance ? 'text-foreground/80' : 'text-muted-foreground'
        )}
        title={
          hasPerformance
            ? `${t('Average latency')}: ${perf?.latency} · ${t('Success rate')}: ${perf?.successRate}`
            : t('No performance data available')
        }
      >
        {perfLoading ? (
          <span className='inline-flex items-center gap-1'>
            <span className='sr-only'>{t('Loading performance data')}</span>
            <span
              aria-hidden='true'
              className='bg-muted-foreground/30 inline-block h-3 w-12 animate-pulse rounded-sm motion-reduce:animate-none'
            />
          </span>
        ) : hasPerformance ? (
          <span className='flex flex-col items-end gap-0.5'>
            <span>{perf?.successRate}</span>
            <span className='text-muted-foreground text-[10px]'>
              {perf?.latency}
            </span>
          </span>
        ) : (
          t('No data')
        )}
      </span>

      <Button
        type='button'
        variant='ghost'
        size='icon-sm'
        className='size-9 shrink-0'
        aria-label={t('Model Square')}
        render={<Link to='/pricing' onClick={onClose} />}
      >
        <ExternalLink className='size-4' />
      </Button>
    </div>
  )
}

function ModelPanelSkeleton() {
  return (
    <div className='divide-border divide-y border-y'>
      {Array.from({ length: 6 }, (_, index) => (
        <div
          key={`model-panel-skeleton-${index}`}
          className='flex items-center gap-3 px-4 py-3'
        >
          <Skeleton className='size-8 rounded-md' />
          <div className='min-w-0 flex-1 space-y-1.5'>
            <Skeleton className='h-3.5 w-40 max-w-full' />
            <Skeleton className='h-3 w-24 max-w-full' />
          </div>
          <Skeleton className='h-3 w-12' />
          <Skeleton className='size-9 rounded-md' />
        </div>
      ))}
    </div>
  )
}

export function ModelPlazaPanel() {
  const { t } = useTranslation()
  const { open, closePanel } = useModelPlaza()
  const pricing = usePricingData({ enabled: open })
  const {
    perfMap,
    isLoading: perfLoading,
    error: perfError,
    refetch: refetchPerf,
  } = usePerfMap({ enabled: open })
  const [query, setQuery] = useState('')
  const [vendor, setVendor] = useState('all')
  const [availability, setAvailability] = useState<AvailabilityFilter>('all')
  const [selectedModel, setSelectedModel] = useState<PricingModel | null>(null)

  const visibleModels = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    return pricing.models.filter((model) => {
      if (vendor !== 'all' && model.vendor_name !== vendor) return false
      const performance = perfMap.get(model.model_name)
      if (
        availability === 'available' &&
        !performance &&
        !perfLoading &&
        !perfError
      ) {
        return false
      }
      if (availability === 'no-data' && performance) return false
      if (!normalizedQuery) return true
      return [
        model.model_name,
        model.vendor_name,
        model.description,
        model.tags,
        model.supported_endpoint_types?.join(' '),
      ]
        .filter(Boolean)
        .some(
          (value) => value?.toLowerCase().includes(normalizedQuery) ?? false
        )
    })
  }, [
    availability,
    perfError,
    perfLoading,
    perfMap,
    pricing.models,
    query,
    vendor,
  ])

  return (
    <>
      <Sheet
        open={open}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) closePanel()
        }}
      >
        <SheetContent side='right' className='w-full gap-0 sm:max-w-xl'>
          <SheetHeader className='border-border/70 border-b pr-14'>
            <SheetTitle className='flex items-center gap-2'>
              <Box className='text-primary size-4' />
              {t('Model Square')}
            </SheetTitle>
            <SheetDescription>{t('Search models')}</SheetDescription>
          </SheetHeader>

          <div className='flex min-h-0 flex-1 flex-col'>
            <div className='border-border/70 space-y-3 border-b p-4'>
              <div className='relative'>
                <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2' />
                <Input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder={t(
                    'Search model name, provider, endpoint, or tag...'
                  )}
                  className='h-10 pl-9'
                  autoFocus
                />
              </div>
              <div className='grid grid-cols-2 gap-2'>
                <Select
                  value={vendor}
                  onValueChange={(value) => value && setVendor(value)}
                >
                  <SelectTrigger className='h-10'>
                    <SelectValue placeholder={t('Provider')} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='all'>{t('All')}</SelectItem>
                    {pricing.vendors.map((item) => (
                      <SelectItem key={item.id} value={item.name}>
                        {item.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Select
                  value={availability}
                  onValueChange={(value) =>
                    value && setAvailability(value as AvailabilityFilter)
                  }
                >
                  <SelectTrigger className='h-10'>
                    <SelectValue placeholder={t('Status')} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='all'>{t('All')}</SelectItem>
                    <SelectItem value='available'>{t('Available')}</SelectItem>
                    <SelectItem value='no-data'>
                      {t('No performance data available')}
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className='text-muted-foreground flex items-center justify-between text-xs'>
                <span>
                  {visibleModels.length} / {pricing.models.length}{' '}
                  {t('Models').toLowerCase()}
                </span>
                <Button
                  variant='link'
                  size='sm'
                  className='h-auto p-0 text-xs'
                  render={<Link to='/pricing' onClick={closePanel} />}
                >
                  {t('Model Square')}
                  <ExternalLink className='ml-1 size-3' />
                </Button>
              </div>
            </div>

            <div className='min-h-0 flex-1 overflow-y-auto'>
              {pricing.isLoading ? (
                <ModelPanelSkeleton />
              ) : pricing.error ? (
                <ErrorState
                  className='min-h-[280px]'
                  title={t('Unable to load live pricing')}
                  onRetry={pricing.refetch}
                />
              ) : visibleModels.length === 0 ? (
                <div className='text-muted-foreground flex min-h-[280px] flex-col items-center justify-center gap-2 px-6 text-center'>
                  <Box className='size-7 opacity-40' />
                  <p className='text-sm font-medium'>{t('No models found')}</p>
                  <p className='text-xs'>
                    {t('No models match your current filters.')}
                  </p>
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => {
                      setQuery('')
                      setVendor('all')
                      setAvailability('all')
                    }}
                  >
                    {t('Clear filters')}
                  </Button>
                </div>
              ) : (
                <>
                  {perfError ? (
                    <Alert className='m-4' role='status'>
                      <AlertTitle>
                        {t('Performance data unavailable')}
                      </AlertTitle>
                      <AlertDescription className='flex flex-wrap items-center gap-x-2 gap-y-1'>
                        <span>
                          {t(
                            'Models are still available without live performance data.'
                          )}
                        </span>
                        <Button
                          type='button'
                          variant='link'
                          size='sm'
                          className='h-auto p-0'
                          onClick={() => void refetchPerf()}
                        >
                          {t('Retry')}
                        </Button>
                      </AlertDescription>
                    </Alert>
                  ) : null}
                  {visibleModels.map((model) => (
                    <ModelRow
                      key={model.model_name}
                      model={model}
                      perf={
                        perfMap.has(model.model_name)
                          ? getModelPerfDisplay(perfMap.get(model.model_name))
                          : undefined
                      }
                      perfLoading={perfLoading}
                      onSelect={() => setSelectedModel(model)}
                      onClose={closePanel}
                    />
                  ))}
                </>
              )}
            </div>
          </div>
        </SheetContent>
      </Sheet>

      {selectedModel && (
        <ModelDetailsDrawer
          open
          onOpenChange={(nextOpen) => {
            if (!nextOpen) setSelectedModel(null)
          }}
          model={selectedModel}
          groupRatio={pricing.groupRatio}
          usableGroup={pricing.usableGroup}
          endpointMap={
            pricing.endpointMap as Record<
              string,
              { path?: string; method?: string }
            >
          }
          autoGroups={pricing.autoGroups}
          priceRate={pricing.priceRate}
          usdExchangeRate={pricing.usdExchangeRate}
          tokenUnit='M'
        />
      )}
    </>
  )
}
