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
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useMemo, useCallback, useEffect, useRef, useState } from 'react'

import {
  FILTER_ALL,
  SORT_OPTIONS,
  QUOTA_TYPES,
  ENDPOINT_TYPES,
  DEFAULT_TOKEN_UNIT,
  VIEW_MODES,
  type ViewMode,
} from '../constants'
import { filterAndSortModels, extractAllTags } from '../lib/filters'
import type { PricingModel, TokenUnit } from '../types'

type FilterState = {
  search?: string
  sort?: string
  vendor?: string
  group?: string
  quotaType?: string
  endpointType?: string
  tag?: string
  tokenUnit?: TokenUnit
  view?: ViewMode
  rechargePrice?: boolean
}

function normalizeViewMode(value: unknown): ViewMode {
  if (value === VIEW_MODES.TABLE) {
    return VIEW_MODES.TABLE
  }
  return VIEW_MODES.CARD
}

export function useFilters(models: PricingModel[]) {
  const search = useSearch({ from: '/pricing/' })
  const navigate = useNavigate({ from: '/pricing/' })
  const searchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const routeFilters = useMemo<FilterState>(
    () => ({
      search: search.search,
      sort: search.sort,
      vendor: search.vendor,
      group: search.group,
      quotaType: search.quotaType,
      endpointType: search.endpointType,
      tag: search.tag,
      tokenUnit: search.tokenUnit,
      view: search.view,
      rechargePrice: search.rechargePrice,
    }),
    [
      search.endpointType,
      search.group,
      search.quotaType,
      search.rechargePrice,
      search.search,
      search.sort,
      search.tag,
      search.tokenUnit,
      search.vendor,
      search.view,
    ]
  )
  const [filterState, setFilterState] = useState<FilterState>(routeFilters)
  const latestSearchRef = useRef(routeFilters.search)

  const cancelPendingSearchUpdate = useCallback(() => {
    if (searchTimerRef.current === null) return
    clearTimeout(searchTimerRef.current)
    searchTimerRef.current = null
  }, [])

  useEffect(() => {
    return cancelPendingSearchUpdate
  }, [cancelPendingSearchUpdate])

  useEffect(() => {
    cancelPendingSearchUpdate()
    latestSearchRef.current = routeFilters.search
    // Keep browser back/forward navigation in sync with the controlled filters.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setFilterState(routeFilters)
  }, [cancelPendingSearchUpdate, routeFilters])

  const searchInput = filterState.search || ''
  const sortBy = filterState.sort || SORT_OPTIONS.NAME
  const vendorFilter = filterState.vendor || FILTER_ALL
  const groupFilter = filterState.group || FILTER_ALL
  const quotaTypeFilter = filterState.quotaType || QUOTA_TYPES.ALL
  const endpointTypeFilter = filterState.endpointType || ENDPOINT_TYPES.ALL
  const tagFilter = filterState.tag || FILTER_ALL
  const tokenUnit: TokenUnit =
    filterState.tokenUnit === 'K' ? 'K' : DEFAULT_TOKEN_UNIT
  const viewMode = normalizeViewMode(filterState.view)
  const showRechargePrice = filterState.rechargePrice === true

  const updateFilters = useCallback(
    (updates: Record<string, unknown>) => {
      const nextSearch = Object.hasOwn(updates, 'search')
        ? typeof updates.search === 'string' && updates.search
          ? updates.search
          : undefined
        : latestSearchRef.current
      const flushedUpdates = { ...updates, search: nextSearch }
      latestSearchRef.current = nextSearch
      cancelPendingSearchUpdate()
      setFilterState((prev) => {
        const next: Record<string, unknown> = { ...prev, ...flushedUpdates }
        for (const key of Object.keys(next)) {
          if (next[key] === undefined || next[key] === null) {
            delete next[key]
          }
        }
        return next as FilterState
      })
      void navigate({
        replace: true,
        search: (previous) => ({ ...previous, ...flushedUpdates }),
      })
    },
    [cancelPendingSearchUpdate, navigate]
  )

  const setSearchInput = useCallback(
    (v: string) => {
      latestSearchRef.current = v || undefined
      setFilterState((previous) => ({ ...previous, search: v || undefined }))
      cancelPendingSearchUpdate()
      searchTimerRef.current = setTimeout(() => {
        searchTimerRef.current = null
        void navigate({
          replace: true,
          search: (previous) => ({ ...previous, search: v || undefined }),
        })
      }, 180)
    },
    [cancelPendingSearchUpdate, navigate]
  )
  const setSortBy = useCallback(
    (v: string) =>
      updateFilters({ sort: v === SORT_OPTIONS.NAME ? undefined : v }),
    [updateFilters]
  )
  const setVendorFilter = useCallback(
    (v: string) => updateFilters({ vendor: v === FILTER_ALL ? undefined : v }),
    [updateFilters]
  )
  const setGroupFilter = useCallback(
    (v: string) => updateFilters({ group: v === FILTER_ALL ? undefined : v }),
    [updateFilters]
  )
  const setQuotaTypeFilter = useCallback(
    (v: string) =>
      updateFilters({ quotaType: v === QUOTA_TYPES.ALL ? undefined : v }),
    [updateFilters]
  )
  const setEndpointTypeFilter = useCallback(
    (v: string) =>
      updateFilters({
        endpointType: v === ENDPOINT_TYPES.ALL ? undefined : v,
      }),
    [updateFilters]
  )
  const setTagFilter = useCallback(
    (v: string) => updateFilters({ tag: v === FILTER_ALL ? undefined : v }),
    [updateFilters]
  )
  const setTokenUnit = useCallback(
    (v: TokenUnit) =>
      updateFilters({ tokenUnit: v === DEFAULT_TOKEN_UNIT ? undefined : v }),
    [updateFilters]
  )
  const setViewMode = useCallback(
    (v: ViewMode) =>
      updateFilters({ view: v === VIEW_MODES.CARD ? undefined : v }),
    [updateFilters]
  )
  const setShowRechargePrice = useCallback(
    (v: boolean) => updateFilters({ rechargePrice: v || undefined }),
    [updateFilters]
  )

  const availableTags = useMemo(() => {
    if (!models || models.length === 0) return []
    return extractAllTags(models)
  }, [models])

  const filteredModels = useMemo(() => {
    if (!models || models.length === 0) return []

    return filterAndSortModels(models, {
      search: searchInput,
      vendor: vendorFilter,
      group: groupFilter,
      quotaType: quotaTypeFilter,
      endpointType: endpointTypeFilter,
      tag: tagFilter,
      sortBy,
    })
  }, [
    models,
    searchInput,
    vendorFilter,
    groupFilter,
    quotaTypeFilter,
    endpointTypeFilter,
    tagFilter,
    sortBy,
  ])

  const hasActiveFilters = useMemo(
    () =>
      vendorFilter !== FILTER_ALL ||
      groupFilter !== FILTER_ALL ||
      quotaTypeFilter !== QUOTA_TYPES.ALL ||
      endpointTypeFilter !== ENDPOINT_TYPES.ALL ||
      tagFilter !== FILTER_ALL,
    [vendorFilter, groupFilter, quotaTypeFilter, endpointTypeFilter, tagFilter]
  )

  const activeFilterCount = useMemo(
    () =>
      (vendorFilter !== FILTER_ALL ? 1 : 0) +
      (groupFilter !== FILTER_ALL ? 1 : 0) +
      (quotaTypeFilter !== QUOTA_TYPES.ALL ? 1 : 0) +
      (endpointTypeFilter !== ENDPOINT_TYPES.ALL ? 1 : 0) +
      (tagFilter !== FILTER_ALL ? 1 : 0),
    [vendorFilter, groupFilter, quotaTypeFilter, endpointTypeFilter, tagFilter]
  )

  const clearFilters = useCallback(() => {
    updateFilters({
      vendor: undefined,
      group: undefined,
      quotaType: undefined,
      endpointType: undefined,
      tag: undefined,
    })
  }, [updateFilters])

  const clearSearch = useCallback(() => {
    updateFilters({ search: undefined })
  }, [updateFilters])

  return {
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
  }
}
