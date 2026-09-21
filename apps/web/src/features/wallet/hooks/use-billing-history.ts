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
import i18next from 'i18next'
import { useState, useEffect, useCallback, useRef } from 'react'
import { toast } from 'sonner'

import { useIsAdmin } from '@/hooks/use-admin'

import {
  getUserBillingHistory,
  getAllBillingHistory,
  completeOrder,
  isApiSuccess,
} from '../api'
import type {
  BillingHistorySortBy,
  BillingHistorySortOrder,
  TopupRecord,
} from '../types'

// ============================================================================
// Billing History Hook
// ============================================================================

interface UseBillingHistoryOptions {
  /** Initial page number */
  initialPage?: number
  /** Initial page size */
  initialPageSize?: number
  /** Initial server-side sort field. */
  initialSortBy?: BillingHistorySortBy
  /** Initial server-side sort direction. */
  initialSortOrder?: BillingHistorySortOrder
  /** Load records only while the owning surface is visible. */
  enabled?: boolean
}

export function useBillingHistory(options: UseBillingHistoryOptions = {}) {
  const {
    initialPage = 1,
    initialPageSize = 10,
    initialSortBy = 'create_time',
    initialSortOrder = 'desc',
    enabled = true,
  } = options
  const isAdmin = useIsAdmin()

  const [records, setRecords] = useState<TopupRecord[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(initialPage)
  const [pageSize, setPageSize] = useState(initialPageSize)
  const [keyword, setKeyword] = useState('')
  const [sortBy, setSortBy] = useState<BillingHistorySortBy>(initialSortBy)
  const [sortOrder, setSortOrder] =
    useState<BillingHistorySortOrder>(initialSortOrder)
  const [loading, setLoading] = useState(false)
  const [completing, setCompleting] = useState(false)
  const activeRequestRef = useRef(0)

  /**
   * Fetch billing history
   */
  const fetchBillingHistory = useCallback(async () => {
    const requestId = ++activeRequestRef.current
    setLoading(true)
    try {
      const response = isAdmin
        ? await getAllBillingHistory(page, pageSize, keyword, sortBy, sortOrder)
        : await getUserBillingHistory(
            page,
            pageSize,
            keyword,
            sortBy,
            sortOrder
          )

      if (requestId !== activeRequestRef.current) return
      if (isApiSuccess(response) && response.data) {
        setRecords(response.data.items || [])
        setTotal(response.data.total || 0)
      } else {
        toast.error(
          response.message || i18next.t('Failed to load billing history')
        )
        setRecords([])
        setTotal(0)
      }
    } catch (error) {
      if (requestId !== activeRequestRef.current) return
      // eslint-disable-next-line no-console
      console.error('Failed to fetch billing history:', error)
      toast.error(i18next.t('Failed to load billing history'))
      setRecords([])
      setTotal(0)
    } finally {
      if (requestId === activeRequestRef.current) {
        setLoading(false)
      }
    }
  }, [isAdmin, page, pageSize, keyword, sortBy, sortOrder])

  /**
   * Complete a pending order (admin only)
   */
  const handleCompleteOrder = useCallback(
    async (tradeNo: string) => {
      if (!isAdmin) {
        toast.error(i18next.t('Admin access required'))
        return false
      }

      setCompleting(true)
      try {
        const response = await completeOrder({ trade_no: tradeNo })
        if (isApiSuccess(response)) {
          toast.success(i18next.t('Order completed successfully'))
          // Refresh the list
          await fetchBillingHistory()
          return true
        } else {
          toast.error(response.message || i18next.t('Failed to complete order'))
          return false
        }
      } catch (error) {
        // eslint-disable-next-line no-console
        console.error('Failed to complete order:', error)
        toast.error(i18next.t('Failed to complete order'))
        return false
      } finally {
        setCompleting(false)
      }
    },
    [isAdmin, fetchBillingHistory]
  )

  /**
   * Change page
   */
  const handlePageChange = useCallback((newPage: number) => {
    setPage(newPage)
  }, [])

  /**
   * Change page size
   */
  const handlePageSizeChange = useCallback((newPageSize: number) => {
    setPageSize(newPageSize)
    setPage(1) // Reset to first page when changing page size
  }, [])

  /**
   * Search by keyword
   */
  const handleSearch = useCallback((newKeyword: string) => {
    setKeyword(newKeyword)
    setPage(1) // Reset to first page when searching
  }, [])

  const handleSortByChange = useCallback((newSortBy: BillingHistorySortBy) => {
    setSortBy(newSortBy)
    setPage(1)
  }, [])

  const handleSortOrderChange = useCallback(
    (newSortOrder: BillingHistorySortOrder) => {
      setSortOrder(newSortOrder)
      setPage(1)
    },
    []
  )

  // Fetch data when dependencies change and invalidate superseded responses.
  useEffect(() => {
    if (!enabled) {
      activeRequestRef.current += 1
      setLoading(false)
      return
    }
    void fetchBillingHistory()
    return () => {
      activeRequestRef.current += 1
    }
  }, [enabled, fetchBillingHistory])

  return {
    records,
    total,
    page,
    pageSize,
    keyword,
    sortBy,
    sortOrder,
    loading,
    completing,
    isAdmin,
    handlePageChange,
    handlePageSizeChange,
    handleSearch,
    handleSortByChange,
    handleSortOrderChange,
    handleCompleteOrder,
    refresh: fetchBillingHistory,
  }
}
