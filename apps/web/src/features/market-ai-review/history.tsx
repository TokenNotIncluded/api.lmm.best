/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { getSettingsErrorMessage } from '@/features/system-settings/utils/settings-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { marketAIReviewAPI, type MarketAIReviewRecord } from './api'
import { MARKET_AI_REVIEW_COPY as copy, MARKET_AI_REVIEW_ERRORS } from './copy'
import { MarketAIReviewResultView } from './result'

function normalizeMarketAIReview(record: MarketAIReviewRecord) {
  return {
    status:
      record.outcome === 'stale'
        ? 'stale'
        : record.status === 'pending'
          ? 'queued'
          : record.status === 'completed'
            ? 'succeeded'
            : record.status,
    decision: record.recommendation ?? undefined,
    applied: record.applied,
    mode: record.mode,
    outcome: record.outcome,
    categories: record.categories ?? [],
    categoriesKnown: record.categories !== null,
    error: record.error_code
      ? Object.hasOwn(MARKET_AI_REVIEW_ERRORS, record.error_code)
        ? MARKET_AI_REVIEW_ERRORS[record.error_code]
        : copy.unavailableError
      : undefined,
    checkedAt: record.completed_at,
  }
}

/** Mount only inside seller/admin surfaces, never the public listing. */
export function MarketAIReviewHistory({
  source,
  id,
  versionId,
  onApplied,
  lazy = false,
  open: controlledOpen,
  onOpenChange,
}: {
  source: 'tool' | 'product'
  id: string
  versionId?: string
  onApplied?: (approved: boolean) => void
  lazy?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const client = useQueryClient()
  const notified = useRef<string | null>(null)
  const [open, setOpen] = useState(!lazy)
  const isOpen = controlledOpen ?? open
  const changeOpen = (next: boolean) => {
    if (onOpenChange) onOpenChange(next)
    else setOpen(next)
  }
  const query = useQuery({
    queryKey: ['market-ai-reviews', user?.id, source, id, versionId],
    queryFn: () => marketAIReviewAPI.records(source, id, versionId),
    enabled: !!user && isOpen,
    retry: false,
    refetchInterval: (current) =>
      typeof document !== 'undefined' &&
      document.visibilityState === 'visible' &&
      current.state.data?.rows.some(
        (row) => row.status === 'pending' || row.status === 'running'
      )
        ? 15000
        : false,
  })
  useEffect(() => {
    const latest = query.data?.rows[0]
    if (
      !latest?.applied ||
      !['approved', 'rejected'].includes(latest.outcome ?? '')
    ) {
      return
    }
    const signature = `${id}:${latest.id}:${latest.outcome}`
    if (notified.current === signature) return
    notified.current = signature
    if (source === 'product') {
      void client.invalidateQueries({ queryKey: ['store', 'my-products'] })
      void client.invalidateQueries({ queryKey: ['store', 'reviews'] })
    } else {
      onApplied?.(latest.outcome === 'approved')
    }
  }, [query.data, source, id, client, onApplied])
  if (!isOpen) {
    return (
      <Button size='sm' variant='outline' onClick={() => changeOpen(true)}>
        {t(copy.result)}
      </Button>
    )
  }
  return (
    <div className='space-y-3'>
      {lazy && (
        <Button size='sm' variant='ghost' onClick={() => changeOpen(false)}>
          {t('Close')}
        </Button>
      )}
      <Button
        size='sm'
        variant='outline'
        disabled={query.isFetching}
        onClick={() => void query.refetch()}
      >
        {t('Refresh')}
      </Button>
      {query.isPending && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t('Loading...')}
        </p>
      )}
      {query.isError && (
        <div role='alert' className='space-y-2 text-sm'>
          <p>{getSettingsErrorMessage(query.error, t(copy.loadError))}</p>
          <Button
            size='sm'
            variant='outline'
            onClick={() => void query.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      )}
      {query.data?.rows.length === 0 && (
        <p className='text-muted-foreground text-sm'>{t(copy.empty)}</p>
      )}
      {query.data?.rows.map((record) => (
        <MarketAIReviewResultView
          key={record.id}
          result={normalizeMarketAIReview(record)}
        />
      ))}
    </div>
  )
}
