/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Search } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useDebounce } from '@/hooks/use-debounce'
import { formatQuota } from '@/lib/format'

import { getAdminPlans, getAdminSubscriptionRecords } from '../api'
import { formatTimestamp } from '../lib'
import { useSubscriptions } from './subscriptions-provider'

const PAGE_SIZE = 20

export function SubscriptionRecords() {
  const { t } = useTranslation()
  const { refreshTrigger } = useSubscriptions()
  const [page, setPage] = useState(1)
  const [query, setQuery] = useState('')
  const [planId, setPlanId] = useState('all')
  const [status, setStatus] = useState('all')
  const debouncedQuery = useDebounce(query.trim(), 300)

  useEffect(() => setPage(1), [debouncedQuery, planId, status])

  const plansQuery = useQuery({
    queryKey: ['admin-subscription-plans', 'with-archived', refreshTrigger],
    queryFn: () => getAdminPlans(true),
    staleTime: 30_000,
  })
  const recordsQuery = useQuery({
    queryKey: [
      'admin-subscription-records',
      page,
      debouncedQuery,
      planId,
      status,
      refreshTrigger,
    ],
    queryFn: ({ signal }) =>
      getAdminSubscriptionRecords(
        {
          page,
          pageSize: PAGE_SIZE,
          query: debouncedQuery || undefined,
          planId: planId === 'all' ? undefined : Number(planId),
          status,
        },
        signal
      ),
    placeholderData: keepPreviousData,
  })

  const records = recordsQuery.data?.data?.items ?? []
  const total = recordsQuery.data?.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const plans = useMemo(
    () => plansQuery.data?.data ?? [],
    [plansQuery.data?.data]
  )

  if (recordsQuery.isError && !recordsQuery.data) {
    return (
      <ErrorState
        title={t('Failed to load subscription records')}
        description={t(
          'Check your connection and retry without losing filters.'
        )}
        onRetry={() => void recordsQuery.refetch()}
      />
    )
  }

  return (
    <div className='space-y-4'>
      <div className='flex flex-col gap-2 sm:flex-row sm:items-center'>
        <div className='relative min-w-0 flex-1'>
          <Search className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2' />
          <Input
            value={query}
            maxLength={200}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t('Search by user, email, or plan')}
            aria-label={t('Search subscription records')}
            className='pl-9'
          />
        </div>
        <Select
          value={planId}
          onValueChange={(value) => value && setPlanId(value)}
        >
          <SelectTrigger
            className='w-full sm:w-56'
            aria-label={t('Filter by plan')}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value='all'>{t('All plans')}</SelectItem>
            {plans.map((record) => (
              <SelectItem key={record.plan.id} value={String(record.plan.id)}>
                {record.plan.title}
                {(record.plan.archived_at ?? 0) > 0
                  ? ` · ${t('Archived')}`
                  : ''}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={status}
          onValueChange={(value) => value && setStatus(value)}
        >
          <SelectTrigger
            className='w-full sm:w-44'
            aria-label={t('Filter by status')}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value='all'>{t('All statuses')}</SelectItem>
            <SelectItem value='active'>{t('Active')}</SelectItem>
            <SelectItem value='expired'>{t('Expired')}</SelectItem>
            <SelectItem value='cancelled'>{t('Invalidated')}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {plansQuery.isError && (
        <div
          className='bg-destructive/10 text-destructive border-destructive/30 flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm'
          role='alert'
        >
          <span>{t('Failed to load plans')}</span>
          <Button
            variant='outline'
            size='sm'
            onClick={() => void plansQuery.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      )}

      <div className='overflow-hidden rounded-md border'>
        <div className='overflow-x-auto'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Subscription')}</TableHead>
                <TableHead>{t('User')}</TableHead>
                <TableHead>{t('Plan')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead className='text-right'>{t('Used quota')}</TableHead>
                <TableHead>{t('Expires at')}</TableHead>
                <TableHead>{t('Source')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {recordsQuery.isPending ? (
                Array.from({ length: 6 }, (_, index) => (
                  <TableRow key={index}>
                    <TableCell colSpan={7}>
                      <Skeleton className='h-7 w-full' />
                    </TableCell>
                  </TableRow>
                ))
              ) : records.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className='h-40 text-center'>
                    <p className='font-medium'>
                      {t('No subscription records')}
                    </p>
                    <p className='text-muted-foreground mt-1 text-sm'>
                      {t('Try changing or clearing the current filters.')}
                    </p>
                    <Button
                      variant='outline'
                      size='sm'
                      className='mt-3'
                      onClick={() => {
                        setQuery('')
                        setPlanId('all')
                        setStatus('all')
                      }}
                    >
                      {t('Reset filters')}
                    </Button>
                  </TableCell>
                </TableRow>
              ) : (
                records.map((record) => (
                  <TableRow key={record.id}>
                    <TableCell>
                      <TableId value={record.id} />
                    </TableCell>
                    <TableCell>
                      <div className='font-medium'>
                        {record.username || `#${record.user_id}`}
                      </div>
                      <div className='text-muted-foreground text-xs'>
                        {record.email || `ID ${record.user_id}`}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div>{record.plan_title || `#${record.plan_id}`}</div>
                      {record.plan_archived_at > 0 && (
                        <StatusBadge
                          label={t('Archived')}
                          variant='neutral'
                          copyable={false}
                        />
                      )}
                    </TableCell>
                    <TableCell>
                      <StatusBadge
                        label={t(
                          record.status === 'cancelled'
                            ? 'Invalidated'
                            : record.status === 'active'
                              ? 'Active'
                              : 'Expired'
                        )}
                        variant={
                          record.status === 'active' ? 'success' : 'neutral'
                        }
                        copyable={false}
                      />
                    </TableCell>
                    <TableCell className='text-right tabular-nums'>
                      {formatQuota(record.amount_used)} /{' '}
                      {formatQuota(record.amount_total)}
                    </TableCell>
                    <TableCell className='text-muted-foreground whitespace-nowrap'>
                      {formatTimestamp(record.end_time)}
                    </TableCell>
                    <TableCell className='text-muted-foreground'>
                      {record.source || '-'}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      <div className='flex items-center justify-between gap-3 text-sm'>
        <span className='text-muted-foreground'>
          {t('{{count}} records', { count: total })}
        </span>
        <div className='flex items-center gap-2'>
          <Button
            variant='outline'
            size='sm'
            disabled={page <= 1 || recordsQuery.isFetching}
            onClick={() => setPage((value) => Math.max(1, value - 1))}
          >
            {t('Previous')}
          </Button>
          <span className='min-w-20 text-center tabular-nums'>
            {page} / {pages}
          </span>
          <Button
            variant='outline'
            size='sm'
            disabled={page >= pages || recordsQuery.isFetching}
            onClick={() => setPage((value) => Math.min(pages, value + 1))}
          >
            {t('Next')}
          </Button>
        </div>
      </div>
    </div>
  )
}
