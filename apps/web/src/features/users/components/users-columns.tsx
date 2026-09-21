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
import type { ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import { BadgeCell } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { LongText } from '@/components/long-text'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatFiatCurrencyAmount } from '@/lib/currency'
import { formatQuota, formatTimestamp } from '@/lib/format'

import {
  USER_STATUS,
  USER_STATUSES,
  USER_ROLES,
  isUserDeleted,
} from '../constants'
import type { User } from '../types'
import { DataTableRowActions } from './data-table-row-actions'
import { UserAssistantHistoryDialog } from './user-assistant-history-dialog'
import { UserAssistantReviewDialog } from './user-assistant-review-dialog'
import { UserQuotaCell } from './user-quota-cell'
import { UserTrustLevelCell } from './user-trust-level-cell'

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

export function useUsersColumns(): ColumnDef<User>[] {
  const { t } = useTranslation()
  return [
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected()}
          indeterminate={table.getIsSomePageRowsSelected()}
          onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
          aria-label={t('Select all')}
          className='translate-y-[2px]'
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(value) => row.toggleSelected(!!value)}
          aria-label={t('Select row')}
          className='translate-y-[2px]'
        />
      ),
      enableSorting: false,
      enableHiding: false,
      size: 40,
    },
    {
      accessorKey: 'id',
      header: t('ID'),
      cell: ({ row }) => {
        return (
          <TableId
            value={row.getValue('id') as number}
            className='w-[60px] text-sm'
          />
        )
      },
      size: 80,
      meta: { mobileOrder: 10 },
    },
    {
      accessorKey: 'username',
      header: t('Username'),
      cell: ({ row }) => {
        const username = row.getValue('username') as string
        const displayName = row.original.display_name
        const remark = row.original.remark
        const paymentRestrictionFlags =
          row.original.payment_restriction_flags || 0
        let linuxDOScoreReason: string | null = null
        if (row.original.linux_do_gamification_score !== undefined) {
          linuxDOScoreReason = t('LinuxDO community score: {{score}}', {
            score: row.original.linux_do_gamification_score,
          })
        } else if (paymentRestrictionFlags & 2) {
          linuxDOScoreReason = t('LinuxDO community score exceeded 10,000')
        }
        const paymentRestrictionReasons = [
          paymentRestrictionFlags & 1
            ? t('Registered with a linux.do email address')
            : null,
          linuxDOScoreReason,
          row.original.linux_do_id ? t('Uses LinuxDO OAuth') : null,
          row.original.disposable_email
            ? t('Disposable email promotion restriction')
            : null,
        ].filter(Boolean) as string[]

        return (
          <div className='flex min-w-[160px] flex-col gap-1'>
            <div className='flex items-center gap-2'>
              <LongText className='max-w-[140px] font-medium'>
                {username}
              </LongText>
              {paymentRestrictionReasons.length > 0 && (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <span
                        className='cursor-help text-amber-500'
                        role='img'
                        aria-label={t('Payment audience profile')}
                      >
                        ★
                      </span>
                    }
                  />
                  <TooltipContent>
                    <p className='mb-1 text-xs font-medium'>
                      {t('Payment audience profile')}
                    </p>
                    {paymentRestrictionReasons.map((reason) => (
                      <p key={reason} className='text-xs'>
                        {reason}
                      </p>
                    ))}
                  </TooltipContent>
                </Tooltip>
              )}
              {remark && (
                <Tooltip>
                  <TooltipTrigger
                    render={<StatusBadge variant='success' copyable={false} />}
                  >
                    <LongText className='max-w-[80px]'>{remark}</LongText>
                  </TooltipTrigger>
                  <TooltipContent>
                    <p className='text-xs'>{remark}</p>
                  </TooltipContent>
                </Tooltip>
              )}
            </div>
            {displayName && displayName !== username && (
              <LongText className='text-muted-foreground max-w-[180px] text-xs'>
                {displayName}
              </LongText>
            )}
          </div>
        )
      },
      enableHiding: false,
      size: 220,
      meta: { mobileTitle: true },
    },
    {
      accessorKey: 'email',
      header: t('Email'),
      cell: ({ row }) => {
        const email = row.original.email?.trim()
        return (
          <LongText className='max-w-[240px] text-sm'>
            {email || t('No email provided')}
          </LongText>
        )
      },
      enableSorting: false,
      size: 240,
      meta: { mobileOrder: 15 },
    },
    {
      accessorKey: 'status',
      header: t('Status'),
      cell: ({ row }) => {
        const user = row.original
        const requestCount = user.request_count

        const statusConfig = isUserDeleted(user)
          ? USER_STATUSES[USER_STATUS.DELETED]
          : USER_STATUSES[user.status as keyof typeof USER_STATUSES]

        if (!statusConfig) {
          return null
        }

        return (
          <Tooltip>
            <TooltipTrigger render={<div className='-ml-1.5 cursor-help' />}>
              <StatusBadge
                label={t(statusConfig.labelKey)}
                variant={statusConfig.variant}
                copyable={false}
              />
            </TooltipTrigger>
            <TooltipContent>
              <p className='text-xs'>
                {t('Requests:')} {requestCount.toLocaleString()}
              </p>
            </TooltipContent>
          </Tooltip>
        )
      },
      filterFn: (row, id, value) => {
        return value.includes(String(row.getValue(id)))
      },
      enableSorting: false,
      size: 120,
      meta: { mobileBadge: true },
    },
    {
      id: 'quota',
      accessorKey: 'quota',
      header: t('Quota'),
      cell: ({ row }) => {
        const user = row.original
        return <UserQuotaCell used={user.used_quota} remaining={user.quota} />
      },
      size: 300,
      minSize: 260,
      meta: { mobileOrder: 40 },
    },
    {
      id: 'topup_quota',
      accessorFn: (row) => row.topup_summary?.quota ?? 0,
      header: t('Top-up'),
      cell: ({ row }) => {
        const summary = row.original.topup_summary
        const methods = summary?.methods ?? []
        return (
          <Tooltip>
            <TooltipTrigger
              render={
                <div className='flex min-w-[150px] cursor-help flex-col gap-0.5 text-sm' />
              }
            >
              <span>{formatQuota(summary?.quota ?? 0)}</span>
              <span className='text-muted-foreground text-xs'>
                {summary?.orders ?? 0} {t('Top-up')}
              </span>
            </TooltipTrigger>
            <TooltipContent className='max-w-[320px]'>
              {methods.length === 0 ? (
                <p className='text-xs'>{formatQuota(0)}</p>
              ) : (
                <div className='space-y-1'>
                  {methods.map((method) => {
                    const label = [
                      method.method.trim(),
                      method.provider?.trim(),
                    ]
                      .filter(Boolean)
                      .join(' · ')
                    return (
                      <p key={`${label}-${method.orders}`} className='text-xs'>
                        {label || '—'}: {formatQuota(method.quota)}
                      </p>
                    )
                  })}
                </div>
              )}
            </TooltipContent>
          </Tooltip>
        )
      },
      size: 170,
      meta: { mobileOrder: 45 },
    },
    {
      id: 'topup_money',
      accessorFn: (row) => row.topup_summary?.money_micros ?? 0,
      header: t('Top-up amount'),
      enableSorting: false,
      cell: ({ row }) => {
        const summary = row.original.topup_summary
        const methods = summary?.methods ?? []
        const currency = summary ? resolveTopupCurrency(summary) : null
        let totalDisplay = '—'
        if (summary?.currency === 'MULTIPLE') {
          totalDisplay = t('Multiple fiat currencies')
        } else if (summary && currency) {
          totalDisplay = formatFiatCurrencyAmount(
            summary.money_micros / 1_000_000,
            currency
          )
        } else if (summary && methods.length > 0) {
          totalDisplay = t('Currency unavailable')
        }
        return (
          <Tooltip>
            <TooltipTrigger
              render={
                <div className='flex min-w-[130px] cursor-help flex-col gap-0.5 text-sm tabular-nums' />
              }
            >
              {totalDisplay}
            </TooltipTrigger>
            <TooltipContent className='max-w-[320px]'>
              {methods.length === 0 ? (
                <p className='text-xs'>—</p>
              ) : (
                <div className='space-y-1'>
                  {methods.map((method) => {
                    const label = [
                      method.method.trim(),
                      method.provider?.trim(),
                    ]
                      .filter(Boolean)
                      .join(' · ')
                    const methodCurrency = method.settlement_currency
                      ?.trim()
                      .toUpperCase()
                    const methodAmount =
                      methodCurrency && methodCurrency !== 'UNKNOWN'
                        ? formatFiatCurrencyAmount(
                            method.money_micros / 1_000_000,
                            methodCurrency
                          )
                        : `${formatUnknownCurrencyAmount(method.money_micros)} (${t('Currency unavailable')})`
                    return (
                      <p
                        key={`${label}-${methodCurrency}-${method.orders}`}
                        className='text-xs'
                      >
                        {label || '—'}: {methodAmount}
                      </p>
                    )
                  })}
                </div>
              )}
            </TooltipContent>
          </Tooltip>
        )
      },
      size: 150,
      meta: { mobileOrder: 46 },
    },
    {
      accessorKey: 'group',
      header: t('Group'),
      cell: ({ row }) => {
        const group = row.getValue('group') as string
        return (
          <BadgeCell>
            <GroupBadge group={group} />
          </BadgeCell>
        )
      },
      filterFn: (row, id, value) => {
        const group = String(row.getValue(id) || t('User Group')).toLowerCase()
        const searchValue = String(value).toLowerCase()
        return group.includes(searchValue)
      },
      size: 140,
      meta: { mobileOrder: 30 },
    },
    {
      accessorKey: 'role',
      header: t('Role'),
      cell: ({ row }) => {
        const roleValue = row.getValue('role') as number
        const roleConfig = USER_ROLES[roleValue as keyof typeof USER_ROLES]

        if (!roleConfig) {
          return null
        }

        return (
          <div className='flex items-center gap-x-2'>
            {roleConfig.icon && (
              <roleConfig.icon size={16} className='text-muted-foreground' />
            )}
            <span className='text-sm'>{t(roleConfig.labelKey)}</span>
          </div>
        )
      },
      filterFn: (row, id, value) => {
        return value.includes(String(row.getValue(id)))
      },
      enableSorting: false,
      size: 120,
      meta: { mobileOrder: 20 },
    },
    {
      id: 'trust_level',
      header: t('Trust level'),
      cell: ({ row }) => {
        return <UserTrustLevelCell user={row.original} />
      },
      enableSorting: false,
      size: 150,
      meta: { mobileBadge: true, mobileOrder: 25 },
    },
    {
      id: 'assistant_profile',
      header: t('AI labels'),
      cell: ({ row }) => {
        const profile = row.original.assistant_profile
        const tags = profile?.tags ?? []
        const source = profile?.source
        if (tags.length === 0) {
          return <span className='text-muted-foreground text-sm'>-</span>
        }
        return (
          <Tooltip>
            <TooltipTrigger
              render={
                <div className='flex max-w-[220px] flex-wrap gap-1 overflow-hidden' />
              }
            >
              {tags.slice(0, 3).map((tag) => (
                <StatusBadge
                  key={tag}
                  label={tag}
                  variant='neutral'
                  copyable={false}
                />
              ))}
              {tags.length > 3 && (
                <StatusBadge
                  label={`+${tags.length - 3}`}
                  variant='neutral'
                  copyable={false}
                />
              )}
            </TooltipTrigger>
            <TooltipContent>
              <p className='mb-1 text-xs font-medium'>{t('AI labels')}</p>
              <p className='text-xs'>{tags.join(', ')}</p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {source === 'assistant'
                  ? t('Generated from assistant conversations')
                  : t('Managed by an administrator')}
              </p>
            </TooltipContent>
          </Tooltip>
        )
      },
      enableSorting: false,
      size: 230,
      meta: { mobileOrder: 28 },
    },
    {
      id: 'assistant_conversations',
      header: t('Support conversations'),
      cell: ({ row }) => <UserAssistantHistoryDialog user={row.original} />,
      enableSorting: false,
      size: 150,
      meta: { mobileOrder: 26 },
    },
    {
      id: 'assistant_violations',
      accessorKey: 'assistant_violation_count',
      header: t('Violations'),
      cell: ({ row }) => <UserAssistantReviewDialog user={row.original} />,
      enableSorting: true,
      size: 130,
      meta: { mobileOrder: 27 },
    },
    {
      id: 'invite_info',
      header: t('Invite Info'),
      cell: ({ row }) => {
        const user = row.original
        const affCount = user.aff_count || 0
        const affHistoryQuota = user.aff_history_quota || 0
        const inviterId = user.inviter_id || 0

        return (
          <div className='flex max-w-full min-w-0 flex-wrap items-center gap-1 overflow-hidden'>
            <Tooltip>
              <TooltipTrigger
                render={
                  <StatusBadge
                    label={`${t('Invited')}: ${affCount}`}
                    variant='neutral'
                    copyable={false}
                    className='cursor-help'
                  />
                }
              />
              <TooltipContent>
                <p className='text-xs'>{t('Number of users invited')}</p>
              </TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger
                render={
                  <StatusBadge
                    label={`${t('Revenue')}: ${formatQuota(affHistoryQuota)}`}
                    variant='neutral'
                    copyable={false}
                    className='cursor-help'
                  />
                }
              />
              <TooltipContent>
                <p className='text-xs'>{t('Total invitation revenue')}</p>
              </TooltipContent>
            </Tooltip>
            {inviterId > 0 && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <StatusBadge
                      label={`${t('Inviter')}: ${inviterId}`}
                      variant='neutral'
                      copyable={false}
                      className='cursor-help'
                    />
                  }
                />
                <TooltipContent>
                  <p className='text-xs'>
                    {t('Invited by user ID')} {inviterId}
                  </p>
                </TooltipContent>
              </Tooltip>
            )}
            {inviterId === 0 && (
              <StatusBadge
                label={t('No Inviter')}
                variant='neutral'
                copyable={false}
              />
            )}
          </div>
        )
      },
      size: 240,
      enableSorting: false,
      meta: { mobileHidden: true },
    },
    {
      accessorKey: 'created_at',
      header: t('Created At'),
      cell: ({ row }) => {
        const ts = row.getValue('created_at') as number | undefined
        return (
          <span className='text-muted-foreground text-sm'>
            {ts ? formatTimestamp(ts) : '-'}
          </span>
        )
      },
      size: 180,
      meta: { mobileHidden: true },
    },
    {
      accessorKey: 'last_login_at',
      header: t('Last Login'),
      cell: ({ row }) => {
        const ts = row.getValue('last_login_at') as number | undefined
        return (
          <span className='text-muted-foreground text-sm'>
            {ts ? formatTimestamp(ts) : '-'}
          </span>
        )
      },
      size: 180,
      meta: { mobileHidden: true },
    },
    {
      id: 'actions',
      header: () => t('Actions'),
      cell: ({ row }) => <DataTableRowActions row={row} />,
      meta: { pinned: 'right' as const },
    },
  ]
}
