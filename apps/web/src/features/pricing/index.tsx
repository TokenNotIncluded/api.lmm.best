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
import { Link } from '@tanstack/react-router'
import { ArrowRight, Wallet } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { usePurchaseEntry } from '@/features/forge/use-purchase-entry'

import {
  EmptyState,
  LoadingSkeleton,
  PricingTable,
  PricingToolbar,
  SearchBar,
  ModelDetailsDrawer,
  VendorIconWall,
  VendorModelSections,
} from './components'
import { EXCLUDED_GROUPS, VIEW_MODES } from './constants'
import { useFilters } from './hooks/use-filters'
import { usePerfMap } from './hooks/use-perf-map'
import { usePricingData } from './hooks/use-pricing-data'

/** Models revealed per "Load more" click on the vendor grid. */
const PAGE_SIZE = 48

/**
 * Model Square, following the gpt.ge models-page structure: a centered page
 * title, a sticky translucent filter bar under the fixed header, and the
 * catalog grouped by vendor with soft card grids.
 */
export function Pricing() {
  const { t } = useTranslation()
  const funding = usePurchaseEntry()
  const [selectedModelName, setSelectedModelName] = useState<string | null>(
    null
  )
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE)

  const {
    models,
    vendors,
    groupRatio,
    usableGroup,
    endpointMap,
    autoGroups,
    isLoading,
    error,
    refetch,
    priceRate,
    usdExchangeRate,
  } = usePricingData()

  const {
    searchInput,
    sortBy,
    vendorFilter,
    groupFilter,
    quotaTypeFilter,
    endpointTypeFilter,
    tagFilter,
    tokenUnit,
    viewMode,
    showRechargePrice,
    setSearchInput,
    setSortBy,
    setVendorFilter,
    setGroupFilter,
    setQuotaTypeFilter,
    setEndpointTypeFilter,
    setTagFilter,
    setTokenUnit,
    setViewMode,
    setShowRechargePrice,
    filteredModels,
    hasActiveFilters,
    activeFilterCount,
    availableTags,
    clearFilters,
    clearSearch,
  } = useFilters(models || [])

  const { perfMap } = usePerfMap()

  const handleModelClick = useCallback((modelName: string) => {
    setSelectedModelName(modelName)
  }, [])

  const selectedModel = useMemo(
    () =>
      selectedModelName
        ? (models || []).find(
            (model) => model.model_name === selectedModelName
          ) || null
        : null,
    [models, selectedModelName]
  )

  const availableGroups = useMemo(
    () =>
      Object.keys(usableGroup || {}).filter(
        (g) => !EXCLUDED_GROUPS.includes(g)
      ),
    [usableGroup]
  )

  const handleClearAll = useCallback(() => {
    clearFilters()
    clearSearch()
  }, [clearFilters, clearSearch])

  // The vendor grid reveals the catalog progressively, gpt.ge-style.
  const visibleModels = useMemo(
    () => filteredModels.slice(0, visibleCount),
    [filteredModels, visibleCount]
  )
  const hasMore = filteredModels.length > visibleModels.length

  const renderPricingContent = () => {
    if (filteredModels.length === 0) {
      return (
        <EmptyState
          searchQuery={searchInput}
          hasActiveFilters={hasActiveFilters}
          onClearFilters={handleClearAll}
          error={error}
          onRetry={refetch}
        />
      )
    }

    if (viewMode === VIEW_MODES.CARD) {
      return (
        <>
          <VendorModelSections
            models={visibleModels}
            onModelClick={handleModelClick}
            priceRate={priceRate}
            usdExchangeRate={usdExchangeRate}
            tokenUnit={tokenUnit}
            showRechargePrice={showRechargePrice}
            selectedGroup={groupFilter}
            perfMap={perfMap}
          />
          {hasMore ? (
            <div className='mt-10 flex justify-center'>
              <button
                type='button'
                onClick={() => setVisibleCount((count) => count + PAGE_SIZE)}
                className='border-foreground/25 hover:bg-muted/70 h-11 rounded-none border px-8 text-sm font-medium transition-colors'
              >
                {t('Load more')}
              </button>
            </div>
          ) : null}
        </>
      )
    }

    return (
      <PricingTable
        models={filteredModels}
        priceRate={priceRate}
        usdExchangeRate={usdExchangeRate}
        tokenUnit={tokenUnit}
        showRechargePrice={showRechargePrice}
        selectedGroup={groupFilter}
        perfMap={perfMap}
        onModelClick={handleModelClick}
      />
    )
  }

  if (isLoading) {
    return (
      <ForgePublicShell>
        <div className='min-h-svh'>
          <div className='mx-auto w-full max-w-7xl px-5 pt-12 pb-10 md:px-10'>
            <LoadingSkeleton viewMode={viewMode} />
          </div>
        </div>
      </ForgePublicShell>
    )
  }

  return (
    <ForgePublicShell>
      <div className='min-h-svh'>
        <div className='mx-auto w-full max-w-7xl px-5 pb-20 md:px-10'>
          {/* Centered page title, gpt.ge-style. */}
          <div className='border-foreground/20 mb-8 border-b pt-12 pb-8 sm:pt-16'>
            <div className='flex flex-col gap-6 sm:flex-row sm:items-end sm:justify-between'>
              <div className='min-w-0'>
                <h1 className='font-serif text-5xl leading-[1.05] font-normal tracking-tight sm:text-6xl'>
                  {t('Models and pricing')}
                </h1>
                <p className='text-muted-foreground mt-4 max-w-2xl text-base leading-7'>
                  {t('This site currently has {{count}} models enabled', {
                    count: models?.length || 0,
                  })}
                </p>
              </div>
              {/* The single most-asked question on this page: how do I pay? */}
              <Button
                size='lg'
                className='h-auto min-h-12 w-full shrink-0 px-6 py-3 text-base sm:w-auto'
                render={
                  <Link
                    to={funding.to}
                    search={
                      funding.to === '/sign-in'
                        ? { redirect: '/wallet' }
                        : undefined
                    }
                  />
                }
              >
                <Wallet className='size-4' aria-hidden='true' />
                {t(funding.label)}
                <ArrowRight className='size-4' aria-hidden='true' />
              </Button>
            </div>
          </div>

          {/* Sticky translucent filter bar: search + compact toolbar. */}
          <div className='bg-background border-foreground/20 sticky top-16 z-40 -mx-5 mb-8 border-b py-3 md:-mx-10'>
            <div className='flex flex-col gap-2 px-5 md:px-10'>
              <SearchBar
                value={searchInput}
                onChange={setSearchInput}
                onClear={clearSearch}
                placeholder={t(
                  'Search model name, provider, endpoint, or tag...'
                )}
                className='mx-auto w-full max-w-2xl'
              />
              <PricingToolbar
                filteredCount={filteredModels.length}
                totalCount={models?.length}
                sortBy={sortBy}
                onSortChange={setSortBy}
                tokenUnit={tokenUnit}
                onTokenUnitChange={setTokenUnit}
                showRechargePrice={showRechargePrice}
                onRechargePriceChange={setShowRechargePrice}
                viewMode={viewMode}
                onViewModeChange={(next) => {
                  setViewMode(next)
                  setVisibleCount(PAGE_SIZE)
                }}
                quotaTypeFilter={quotaTypeFilter}
                endpointTypeFilter={endpointTypeFilter}
                vendorFilter={vendorFilter}
                groupFilter={groupFilter}
                tagFilter={tagFilter}
                onQuotaTypeChange={setQuotaTypeFilter}
                onEndpointTypeChange={setEndpointTypeFilter}
                onVendorChange={setVendorFilter}
                onGroupChange={setGroupFilter}
                onTagChange={setTagFilter}
                vendors={vendors || []}
                groups={availableGroups}
                groupRatios={groupRatio}
                tags={availableTags}
                models={models || []}
                hasActiveFilters={hasActiveFilters}
                activeFilterCount={activeFilterCount}
                onClearFilters={() => {
                  clearFilters()
                  setVisibleCount(PAGE_SIZE)
                }}
              />
            </div>
          </div>

          <VendorIconWall
            vendors={vendors || []}
            models={models || []}
            activeVendor={vendorFilter}
            onVendorChange={(next) => {
              setVendorFilter(next)
              setVisibleCount(PAGE_SIZE)
            }}
          />

          <main className='min-w-0'>{renderPricingContent()}</main>
        </div>

        {selectedModel && (
          <ModelDetailsDrawer
            open={Boolean(selectedModel)}
            onOpenChange={(open) => {
              if (!open) setSelectedModelName(null)
            }}
            model={selectedModel}
            groupRatio={groupRatio || {}}
            usableGroup={usableGroup || {}}
            endpointMap={
              (endpointMap as Record<
                string,
                { path?: string; method?: string }
              >) || {}
            }
            autoGroups={autoGroups || []}
            priceRate={priceRate ?? 1}
            usdExchangeRate={usdExchangeRate ?? 1}
            tokenUnit={tokenUnit}
            showRechargePrice={showRechargePrice}
          />
        )}
      </div>
    </ForgePublicShell>
  )
}
