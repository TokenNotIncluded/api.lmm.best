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
import type { Table as TanstackTable } from '@tanstack/react-table'
import { CalendarClock, Database, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { DISABLED_ROW_MOBILE } from '@/components/data-table'
import { MaskedValueDisplay } from '@/components/masked-value-display'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  REDEMPTION_EXPIRED_STATUS_ICON,
  REDEMPTION_STATUS,
  REDEMPTION_STATUSES,
} from '../constants'
import { isRedemptionExpired } from '../lib'
import type { Redemption } from '../types'
import { DataTableRowActions } from './data-table-row-actions'
import { useRedemptions } from './redemptions-provider'

const MOBILE_SKELETON_KEYS = [
  'redemption-mobile-skeleton-1',
  'redemption-mobile-skeleton-2',
  'redemption-mobile-skeleton-3',
  'redemption-mobile-skeleton-4',
  'redemption-mobile-skeleton-5',
]

function RedemptionsMobileSkeleton() {
  return (
    <div className='divide-border overflow-hidden rounded-lg border'>
      {MOBILE_SKELETON_KEYS.map((key) => (
        <div
          key={key}
          className='space-y-2 border-b px-3 py-2.5 last:border-b-0'
        >
          <div className='flex items-center justify-between'>
            <Skeleton className='h-4 w-32' />
            <Skeleton className='h-5 w-16 rounded-md' />
          </div>
          <div className='flex items-center justify-between gap-3'>
            <Skeleton className='h-7 w-44' />
            <Skeleton className='h-8 w-16' />
          </div>
          <Skeleton className='h-3 w-28' />
        </div>
      ))}
    </div>
  )
}

interface RedemptionsMobileListProps {
  table: TanstackTable<Redemption>
  isLoading: boolean
  isFetching?: boolean
}

export function RedemptionsMobileList(props: RedemptionsMobileListProps) {
  const { t } = useTranslation()
  const { setOpen } = useRedemptions()
  const rows = props.table.getRowModel().rows

  if (props.isLoading) return <RedemptionsMobileSkeleton />

  if (!rows.length) {
    return (
      <div className='rounded-lg border p-6'>
        <Empty className='border-none p-0'>
          <EmptyHeader>
            <EmptyMedia variant='icon'>
              <Database className='size-6' />
            </EmptyMedia>
            <EmptyTitle>{t('No Redemption Codes Found')}</EmptyTitle>
            <EmptyDescription>
              {t(
                'No redemption codes available. Create your first redemption code to get started.'
              )}
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button
              className='h-11 w-full gap-2 sm:h-9 sm:w-auto'
              onClick={() => setOpen('create')}
            >
              <Plus className='size-4' />
              {t('Create Code')}
            </Button>
          </EmptyContent>
        </Empty>
      </div>
    )
  }

  return (
    <div
      className={cn(
        'divide-border overflow-hidden rounded-lg border',
        props.isFetching && 'pointer-events-none opacity-60'
      )}
    >
      {rows.map((row) => {
        const redemption = row.original
        const expired = isRedemptionExpired(
          redemption.expired_time,
          redemption.status
        )
        const statusConfig = REDEMPTION_STATUSES[redemption.status]
        const maskedKey = `${redemption.key.slice(0, 8)}******${redemption.key.slice(-8)}`
        const inactive =
          expired || redemption.status !== REDEMPTION_STATUS.ENABLED

        return (
          <div
            key={row.id}
            aria-disabled={inactive || undefined}
            className={cn(
              'bg-card space-y-2.5 border-b px-3 py-2.5 last:border-b-0',
              inactive ? DISABLED_ROW_MOBILE : undefined
            )}
          >
            <div className='flex items-start justify-between gap-3'>
              <div className='min-w-0'>
                <div className='truncate text-sm font-semibold'>
                  {redemption.name}
                </div>
                <div className='text-muted-foreground text-[11px]'>
                  {t('Redemption Code')}
                </div>
              </div>
              {expired ? (
                <StatusBadge
                  label={t('Expired')}
                  variant='warning'
                  icon={REDEMPTION_EXPIRED_STATUS_ICON}
                  copyable={false}
                />
              ) : (
                statusConfig && (
                  <StatusBadge
                    label={t(statusConfig.labelKey)}
                    variant={statusConfig.variant}
                    icon={statusConfig.icon}
                    copyable={false}
                  />
                )
              )}
            </div>

            <div className='flex min-w-0 items-center justify-between gap-2'>
              <div className='min-w-0 flex-1 [&_button:first-child]:max-w-full [&_button:first-child]:truncate [&_button:first-child]:px-0'>
                <MaskedValueDisplay
                  label={t('Full Code')}
                  fullValue={redemption.key}
                  maskedValue={maskedKey}
                  copyTooltip={t('Copy code')}
                  copyAriaLabel={t('Copy redemption code')}
                />
              </div>
              <DataTableRowActions row={row} />
            </div>

            <div className='flex items-center justify-between gap-2 text-xs'>
              <span className='text-muted-foreground'>{t('Quota')}</span>
              <span className='font-medium tabular-nums'>
                {formatQuota(redemption.quota)}
              </span>
            </div>

            {redemption.expired_time > 0 && (
              <div className='text-muted-foreground flex items-center justify-between gap-2 text-xs'>
                <span className='inline-flex items-center gap-1.5'>
                  <CalendarClock
                    className='size-3.5 shrink-0'
                    aria-hidden='true'
                  />
                  {t('Expires')}
                </span>
                <span
                  className={cn(
                    'font-mono tabular-nums',
                    expired && 'text-destructive'
                  )}
                >
                  {formatTimestampToDate(redemption.expired_time)}
                </span>
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
