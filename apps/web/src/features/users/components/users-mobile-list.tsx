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
/*
Copyright (C) 2026 LIghtJUNction
*/
import type { Row, Table } from '@tanstack/react-table'
import { ChevronDown, Database } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
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
import {
  formatCumulativeUserUsage,
  formatRawCreditCount,
} from '@/lib/cumulative-user-usage'
import { getCurrencyFormattingLocale } from '@/lib/currency'
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
import { TopupCreditsValue, TopupPaymentValue } from './topup-values'
import { UserAssistantHistoryDialog } from './user-assistant-history-dialog'
import { UserAssistantReviewDialog } from './user-assistant-review-dialog'
import { UserQuotaCell } from './user-quota-cell'
import { UserRiskCell } from './user-risk-cell'
import { UserTrustLevelCell } from './user-trust-level-cell'

type UsersMobileListProps = {
  table: Table<User>
  isLoading?: boolean
  isFetching?: boolean
  emptyTitle: string
  emptyDescription: string
  /** Next-step actions offered when the list has no rows. */
  emptyAction?: React.ReactNode
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
      <div className='text-muted-foreground text-xs leading-normal'>
        {label}
      </div>
      <div className='min-w-0 text-sm [overflow-wrap:anywhere]'>{children}</div>
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
      icon={statusConfig.icon}
      copyable={false}
    />
  )
}

function UserMobileRow({ row }: { row: Row<User> }) {
  const { t, i18n } = useTranslation()
  const rawCreditLocale = getCurrencyFormattingLocale(
    i18n.resolvedLanguage || i18n.language
  )
  const [expanded, setExpanded] = useState(false)
  const detailsId = useId()
  const selectionId = useId()
  const user = row.original
  const email = user.email?.trim()
  const displayName = user.display_name?.trim()
  const topup = user.topup_summary
  const disabled = isUserDeleted(user) || user.status === USER_STATUS.DISABLED

  return (
    <article
      className={cn(
        'min-w-0 px-3 py-3 transition-colors',
        row.getIsSelected() && 'bg-primary/5',
        disabled && 'bg-muted/30 text-muted-foreground'
      )}
    >
      <div className='flex min-w-0 items-start justify-between gap-3'>
        <div className='flex min-w-0 flex-1 items-start gap-1'>
          <Label
            htmlFor={selectionId}
            className='-mt-2 -ml-2 grid size-11 shrink-0 cursor-pointer place-items-center'
          >
            <Checkbox
              id={selectionId}
              checked={row.getIsSelected()}
              onCheckedChange={(value) => row.toggleSelected(!!value)}
              aria-label={`${t('Select row')}: ${user.username}`}
            />
          </Label>
          <div className='min-w-0 flex-1'>
            <div className='flex min-w-0 items-baseline gap-2'>
              <span className='min-w-0 text-base font-medium [overflow-wrap:anywhere]'>
                {user.username}
              </span>
              <span className='text-muted-foreground shrink-0 font-mono text-[11px] tabular-nums'>
                #{user.id}
              </span>
            </div>
            {displayName && displayName !== user.username && (
              <div className='text-muted-foreground mt-0.5 text-sm [overflow-wrap:anywhere]'>
                {displayName}
              </div>
            )}
            <div
              className='text-muted-foreground mt-1 text-sm [overflow-wrap:anywhere]'
              title={email || t('No email provided')}
            >
              {email || t('No email provided')}
            </div>
          </div>
        </div>
        <div className='shrink-0'>{getStatusBadge(user, t)}</div>
      </div>

      <div className='text-muted-foreground mt-2 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs'>
        <GroupBadge group={user.group} />
        <span>{getRoleLabel(user, t)}</span>
        {user.wallet_risk?.high_risk && (
          <StatusBadge
            label={t('High risk')}
            variant='danger'
            copyable={false}
          />
        )}
      </div>
      <div className='mt-2 grid grid-cols-2 gap-3'>
        <MobileMetric label={t('Remaining:')}>
          <span className='font-medium tabular-nums'>
            {formatQuota(user.quota)}
          </span>
        </MobileMetric>
        <MobileMetric label={t('Actual payment')}>
          <span className='font-medium tabular-nums'>
            <TopupPaymentValue record={topup} />
          </span>
        </MobileMetric>
      </div>
      <div className='mt-2 flex min-w-0 items-center justify-between gap-2'>
        <Button
          variant='ghost'
          className='h-11 gap-1 px-2'
          aria-expanded={expanded}
          aria-controls={detailsId}
          onClick={() => setExpanded((value) => !value)}
        >
          {t('Details')}
          <ChevronDown
            aria-hidden='true'
            className={cn(
              'size-4 transition-transform motion-reduce:transition-none',
              expanded && 'rotate-180'
            )}
          />
        </Button>
        <DataTableRowActions row={row} />
      </div>
      <div id={detailsId} hidden={!expanded} className='mt-2 border-t pt-3'>
        <div className='mb-3 flex flex-wrap items-center justify-between gap-2 [&_button]:min-h-11 [&_button]:min-w-11'>
          <UserRiskCell user={user} />
          <UserTrustLevelCell user={user} />
        </div>
        <div className='space-y-3'>
          <MobileMetric label={t('Quota')}>
            <div className='[&_.truncate]:overflow-visible [&_.truncate]:[overflow-wrap:anywhere] [&_.truncate]:whitespace-normal'>
              <UserQuotaCell
                used={user.used_quota}
                normalizedUsed={user.normalized_used_quota}
                projectionAvailable={user.usage_projection_available}
                remaining={user.quota}
                transferred={user.wallet_risk?.transferred_quota}
              />
            </div>
            <p className='text-muted-foreground mt-1 text-xs'>
              {t('Used:')}{' '}
              {formatCumulativeUserUsage(
                user,
                formatQuota,
                t('Credits'),
                rawCreditLocale
              )}
            </p>
            <p className='text-muted-foreground text-xs'>
              {t('Transferred out')}:{' '}
              {formatRawCreditCount(
                user.wallet_risk?.transferred_quota ?? 0,
                t('Credits'),
                rawCreditLocale
              )}{' '}
              · {t('Received transfers')}:{' '}
              {formatRawCreditCount(
                user.wallet_risk?.received_quota ?? 0,
                t('Credits'),
                rawCreditLocale
              )}
            </p>
          </MobileMetric>
          <MobileMetric label={t('Actual payment')}>
            <div className='tabular-nums'>
              <TopupPaymentValue record={topup} />
            </div>
            <div className='text-muted-foreground mt-0.5 text-xs tabular-nums'>
              {t('Top-up credits')}: <TopupCreditsValue record={topup} /> ·{' '}
              {topup?.orders ?? 0}
            </div>
            {topup?.methods && topup.methods.length > 0 ? (
              <details className='mt-1 text-xs'>
                <summary className='text-muted-foreground inline-flex min-h-11 cursor-pointer items-center py-2 underline decoration-dotted underline-offset-2'>
                  {t('Payment method')} · {topup.methods.length}
                </summary>
                <div className='border-muted-foreground/30 mt-1 space-y-1 border-l pl-2'>
                  {topup.methods.map((method) => {
                    const label = [
                      method.method.trim(),
                      method.provider?.trim(),
                    ]
                      .filter(Boolean)
                      .join(' · ')
                    return (
                      <div
                        key={`${label}-${method.settlement_currency}-${method.orders}`}
                        className='min-w-0 space-y-1'
                      >
                        <span className='block'>{label || '—'}</span>
                        <span className='block tabular-nums'>
                          <TopupPaymentValue record={method} />
                          <span className='text-muted-foreground block text-[11px]'>
                            <TopupCreditsValue record={method} /> ·{' '}
                            {method.orders}
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
  emptyAction,
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
          {emptyAction ? <EmptyContent>{emptyAction}</EmptyContent> : null}
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
