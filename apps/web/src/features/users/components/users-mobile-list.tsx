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
import type { Row, Table } from '@tanstack/react-table'
import { Database } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { formatFiatCurrencyAmount } from '@/lib/currency'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  USER_ROLES,
  USER_STATUS,
  USER_STATUSES,
  isUserDeleted,
} from '../constants'
import type { User } from '../types'
import { DataTableRowActions } from './data-table-row-actions'
import { UserAssistantHistoryDialog } from './user-assistant-history-dialog'
import { UserAssistantReviewDialog } from './user-assistant-review-dialog'
import { UserQuotaCell } from './user-quota-cell'
import { UserTrustLevelCell } from './user-trust-level-cell'

type UsersMobileListProps = {
  table: Table<User>
  isLoading?: boolean
  isFetching?: boolean
  emptyTitle: string
  emptyDescription: string
}

function MobileListSkeleton() {
  return (
    <div className='divide-y overflow-hidden rounded-lg border'>
      {[1, 2, 3, 4].map((item) => (
        <div key={item} className='space-y-3 px-3 py-4'>
          <div className='flex items-start justify-between gap-3'>
            <div className='min-w-0 flex-1 space-y-2'>
              <Skeleton className='h-4 w-32' />
              <Skeleton className='h-3 w-48 max-w-full' />
            </div>
            <Skeleton className='h-5 w-14' />
          </div>
          <div className='grid grid-cols-2 gap-3'>
            <Skeleton className='h-12' />
            <Skeleton className='h-12' />
          </div>
        </div>
      ))}
    </div>
  )
}

function MobileMetric({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className='min-w-0 space-y-1'>
      <div className='text-muted-foreground text-[10px] leading-none tracking-wide uppercase'>
        {label}
      </div>
      <div className='min-w-0 overflow-hidden text-sm'>{children}</div>
    </div>
  )
}

function getRoleLabel(user: User, t: (key: string) => string) {
  const role = USER_ROLES[user.role as keyof typeof USER_ROLES]
  return role ? t(role.labelKey) : t('User')
}

function getStatusBadge(user: User, t: (key: string) => string) {
  const statusConfig = isUserDeleted(user)
    ? USER_STATUSES[USER_STATUS.DELETED]
    : USER_STATUSES[user.status as keyof typeof USER_STATUSES]

  if (!statusConfig) return null

  return (
    <StatusBadge
      label={t(statusConfig.labelKey)}
      variant={statusConfig.variant}
      copyable={false}
    />
  )
}

function resolveTopupCurrency(
  summary: NonNullable<User['topup_summary']>
): string | null {
  const preferred = summary.currency?.trim().toUpperCase()
  if (preferred && preferred !== 'MULTIPLE' && preferred !== 'UNKNOWN') {
    return preferred
  }
  const currencies = new Set(
    summary.methods
      .map((method) => method.settlement_currency?.trim().toUpperCase())
      .filter((currency): currency is string =>
        Boolean(currency && currency !== 'UNKNOWN')
      )
  )
  if (!preferred && currencies.size === 1) return [...currencies][0]
  return null
}

function formatUnknownCurrencyAmount(micros: number): string {
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 6,
  }).format(micros / 1_000_000)
}

function UserMobileRow({ row }: { row: Row<User> }) {
  const { t } = useTranslation()
  const user = row.original
  const email = user.email?.trim()
  const displayName = user.display_name?.trim()
  const topup = user.topup_summary
  const topupCurrency = topup ? resolveTopupCurrency(topup) : null
  let topupMoneyDisplay = '—'
  if (topup?.currency === 'MULTIPLE') {
    topupMoneyDisplay = t('Multiple fiat currencies')
  } else if (topup && topupCurrency) {
    topupMoneyDisplay = formatFiatCurrencyAmount(
      topup.money_micros / 1_000_000,
      topupCurrency
    )
  } else if (topup?.methods.length) {
    topupMoneyDisplay = t('Currency unavailable')
  }
  const disabled = isUserDeleted(user) || user.status === USER_STATUS.DISABLED

  return (
    <article
      className={cn(
        'min-w-0 px-3 py-4 transition-colors',
        disabled && 'bg-muted/30 text-muted-foreground'
      )}
    >
      <div className='flex min-w-0 items-start justify-between gap-3'>
        <div className='flex min-w-0 flex-1 items-start gap-2.5'>
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(!!value)}
            aria-label={`${t('Select row')}: ${user.username}`}
            className='mt-0.5 shrink-0'
          />
          <div className='min-w-0 flex-1'>
            <div className='flex min-w-0 items-baseline gap-2'>
              <span className='min-w-0 truncate text-sm font-medium'>
                {user.username}
              </span>
              <span className='text-muted-foreground shrink-0 font-mono text-[11px] tabular-nums'>
                #{user.id}
              </span>
            </div>
            {displayName && displayName !== user.username && (
              <div className='text-muted-foreground mt-0.5 truncate text-xs'>
                {displayName}
              </div>
            )}
            <div
              className='text-muted-foreground mt-1 text-xs [overflow-wrap:anywhere]'
              title={email || t('No email provided')}
            >
              {email || t('No email provided')}
            </div>
          </div>
        </div>
        <div className='flex max-w-[42%] shrink-0 flex-wrap justify-end gap-1'>
          {getStatusBadge(user, t)}
          <UserTrustLevelCell user={user} />
        </div>
      </div>

      <div className='mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-t pt-3'>
        <MobileMetric label={t('Group')}>
          <GroupBadge group={user.group} />
        </MobileMetric>
        <MobileMetric label={t('Role')}>
          <span className='truncate'>{getRoleLabel(user, t)}</span>
        </MobileMetric>
        <MobileMetric label={t('Quota')}>
          <UserQuotaCell used={user.used_quota} remaining={user.quota} />
        </MobileMetric>
        <MobileMetric label={t('Top-up')}>
          <div className='truncate tabular-nums'>{topupMoneyDisplay}</div>
          <div className='text-muted-foreground mt-0.5 truncate text-xs tabular-nums'>
            {formatQuota(topup?.quota ?? 0)} · {topup?.orders ?? 0}
          </div>
          {topup?.methods && topup.methods.length > 0 ? (
            <details className='mt-1 text-xs'>
              <summary className='text-muted-foreground inline-flex min-h-11 cursor-pointer items-center py-2 underline decoration-dotted underline-offset-2'>
                {t('Payment method')} · {topup.methods.length}
              </summary>
              <div className='border-muted-foreground/30 mt-1 space-y-1 border-l pl-2'>
                {topup.methods.map((method) => {
                  const label = [method.method.trim(), method.provider?.trim()]
                    .filter(Boolean)
                    .join(' · ')
                  const currency = method.settlement_currency
                    ?.trim()
                    .toUpperCase()
                  const amount =
                    currency && currency !== 'UNKNOWN'
                      ? formatFiatCurrencyAmount(
                          method.money_micros / 1_000_000,
                          currency
                        )
                      : `${formatUnknownCurrencyAmount(method.money_micros)} (${t('Currency unavailable')})`
                  return (
                    <div
                      key={`${label}-${currency}-${method.orders}`}
                      className='flex min-w-0 items-baseline justify-between gap-2'
                    >
                      <span className='min-w-0 truncate'>{label || '—'}</span>
                      <span className='shrink-0 text-right tabular-nums'>
                        {amount}
                        <span className='text-muted-foreground block text-[11px]'>
                          {formatQuota(method.quota)} · {method.orders}
                        </span>
                      </span>
                    </div>
                  )
                })}
              </div>
            </details>
          ) : null}
        </MobileMetric>
      </div>

      <div className='mt-3 flex min-w-0 items-center justify-between gap-2 border-t pt-3'>
        <div className='flex min-w-0 flex-wrap items-center gap-1.5'>
          {user.assistant_conversation_count !== undefined && (
            <UserAssistantHistoryDialog user={user} />
          )}
          <UserAssistantReviewDialog user={user} />
        </div>
        <div className='shrink-0'>
          <DataTableRowActions row={row} />
        </div>
      </div>
    </article>
  )
}

export function UsersMobileList({
  table,
  isLoading = false,
  isFetching = false,
  emptyTitle,
  emptyDescription,
}: UsersMobileListProps) {
  const rows = table.getRowModel().rows

  if (isLoading) return <MobileListSkeleton />

  if (rows.length === 0) {
    return (
      <div className='rounded-lg border p-6'>
        <Empty className='border-none p-0'>
          <EmptyHeader>
            <EmptyMedia variant='icon'>
              <Database className='size-6' />
            </EmptyMedia>
            <EmptyTitle>{emptyTitle}</EmptyTitle>
            <EmptyDescription>{emptyDescription}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    )
  }

  return (
    <div
      className={cn(
        'divide-y overflow-hidden rounded-lg border',
        isFetching && 'pointer-events-none opacity-60'
      )}
    >
      {rows.map((row) => (
        <UserMobileRow key={row.id} row={row} />
      ))}
    </div>
  )
}
