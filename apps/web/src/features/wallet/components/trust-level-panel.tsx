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
  ChartIncreaseIcon,
  Clock01Icon,
  Crown02Icon,
  DiscountTag01Icon,
  ShieldUserIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import {
  formatMinimumQuotaInCurrency,
  getCurrencyFormattingLocale,
} from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import type { TrustLevelTier } from '@/stores/auth-store'

import type { UserWalletData } from '../types'
import { getTrustLevelProgress, parseTrustCredits } from './trust-level-display'

interface TrustLevelPanelProps {
  user: UserWalletData | null
  loading?: boolean
  onRefresh?: () => Promise<void> | void
  refreshing?: boolean
  refreshError?: boolean
}

function formatDiscount(percent: number) {
  return `${Number(percent.toFixed(6))}%`
}

function benefitLabel(code: string, t: (key: string) => string) {
  switch (code) {
    case 'developer_access':
      return t('Developer console access')
    case 'usage_discount':
      return t('Usage discount')
    case 'standard_access':
      return t('Standard access')
    case 'administrator_access':
      return t('Administrator access')
    case 'superadministrator_access':
      return t('Super administrator access')
    default:
      return code
  }
}

function formatTierBenefits(tier: TrustLevelTier, t: (key: string) => string) {
  if (tier.benefits_hidden) {
    return `${tier.benefit_count ?? 0} ${t('benefits hidden')}`
  }
  if (tier.benefits?.length) {
    return tier.benefits.map((benefit) => benefitLabel(benefit, t)).join(' · ')
  }
  return t('No additional benefits')
}

export function TrustLevelPanel({
  user,
  loading = false,
  onRefresh,
  refreshing = false,
  refreshError = false,
}: TrustLevelPanelProps) {
  const { t, i18n } = useTranslation()
  const currency = useWalletCurrency()
  const info = user?.trust_level_info
  const tiers = user?.trust_level_tiers ?? []

  if (loading) {
    return (
      <section className='bg-card overflow-hidden rounded-none border'>
        <div className='grid gap-6 p-5 lg:grid-cols-[minmax(0,1.1fr)_minmax(300px,0.9fr)] lg:p-6'>
          <div className='space-y-4'>
            <Skeleton className='h-4 w-32' />
            <Skeleton className='h-12 w-24' />
            <Skeleton className='h-2 w-full' />
            <div className='grid grid-cols-2 gap-3 sm:grid-cols-3'>
              <Skeleton className='h-16' />
              <Skeleton className='h-16' />
              <Skeleton className='h-16' />
            </div>
          </div>
          <Skeleton className='min-h-40 w-full' />
        </div>
      </section>
    )
  }

  const {
    currentLevel,
    automaticLevel,
    automaticTiers,
    nextLevel,
    progress,
    roleAssigned,
  } = getTrustLevelProgress(info, tiers, user?.role)
  const recoveryLevel =
    !roleAssigned &&
    !info?.overridden &&
    automaticLevel != null &&
    automaticLevel > currentLevel &&
    (info?.inactivity_decay_steps ?? 0) > 0
      ? automaticLevel
      : null
  const formatPaidCredits = (
    credits: string | null | undefined,
    minimum = false
  ) => {
    const exact = parseTrustCredits(credits)
    if (exact == null) return '-'
    if (exact <= BigInt(Number.MAX_SAFE_INTEGER)) {
      return minimum
        ? formatMinimumQuotaInCurrency(
            Number(exact),
            currency.currency,
            {
              locale: getCurrencyFormattingLocale(
                i18n.resolvedLanguage || i18n.language
              ),
              creditLabel: currency.label,
            },
            currency.config
          )
        : currency.formatQuota(Number(exact))
    }
    return `${exact.toLocaleString(i18n.resolvedLanguage || i18n.language)} ${t('Credits')}`
  }
  let decayLabel = t('No further decay at the current level')
  if (roleAssigned) {
    decayLabel = t('Assigned by account role')
  } else if (info?.overridden) {
    decayLabel = t('Paused while an administrator override is active')
  } else if (info?.next_decay_at) {
    decayLabel = t('Next review {{date}}', {
      date: formatTimestampToDate(info.next_decay_at),
    })
  }
  let statusLabel = t('Automatic')
  if (roleAssigned) {
    statusLabel = t('Role-assigned access')
  } else if (info?.overridden) {
    statusLabel = t('Administrator override')
  }

  return (
    <section className='bg-card overflow-hidden rounded-none border'>
      <div className='grid gap-6 p-5 lg:grid-cols-[minmax(0,1.1fr)_minmax(300px,0.9fr)] lg:p-6'>
        <div className='min-w-0'>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div className='flex items-center gap-2'>
              <HugeiconsIcon
                icon={roleAssigned ? Crown02Icon : ShieldUserIcon}
                className='text-primary size-5'
                aria-hidden='true'
              />
              <div>
                <p className='text-sm font-semibold'>
                  {t('Levels & Benefits')}
                </p>
                <p className='text-muted-foreground text-xs'>
                  {roleAssigned
                    ? t('Role-assigned access')
                    : t('Benefits grow with account history')}
                </p>
              </div>
            </div>
            <div className='flex flex-wrap items-center gap-2'>
              <Badge variant={info?.overridden ? 'warning' : 'outline'}>
                {statusLabel}
              </Badge>
              {onRefresh ? (
                <Button
                  variant='ghost'
                  size='sm'
                  disabled={refreshing}
                  onClick={() => void onRefresh()}
                >
                  {t(refreshing ? 'Refreshing...' : 'Refresh account status')}
                </Button>
              ) : null}
            </div>
          </div>
          {refreshError ? (
            <p role='status' className='text-muted-foreground mt-2 text-xs'>
              {t('Refresh failed')}
            </p>
          ) : null}

          <div className='mt-6 flex items-end gap-3'>
            <span className='font-mono text-5xl leading-none font-semibold tracking-tight tabular-nums'>
              L{currentLevel}
            </span>
            <span className='text-muted-foreground mb-1 text-sm'>
              {formatDiscount(info?.discount_percent ?? 0)}{' '}
              {t('usage discount')}
            </span>
          </div>

          {roleAssigned ? (
            <p
              className='text-muted-foreground mt-6 text-sm leading-6'
              data-testid='role-level-explanation'
            >
              {t(
                'L5 and L6 are administrator roles. Recharges only change automatic levels L0–L4 and cannot grant administrator access.'
              )}
            </p>
          ) : info?.paid_credit_projection_available === false ? (
            <p className='text-muted-foreground mt-6 text-sm'>
              {t('Cumulative eligible recharge')}: {t('Unavailable')}
            </p>
          ) : nextLevel != null ? (
            <div
              className='mt-6 space-y-2'
              data-testid='recharge-level-progress'
            >
              <div className='flex items-center justify-between gap-3 text-xs'>
                <span className='text-muted-foreground'>
                  {t('Progress to L{{level}}', { level: nextLevel })}
                </span>
                <span className='font-medium tabular-nums'>
                  {progress == null ? '-' : `${Math.round(progress)}%`}
                </span>
              </div>
              {progress != null && (
                <Progress value={progress} className='h-2' />
              )}
              <div className='text-muted-foreground flex flex-wrap justify-between gap-x-4 gap-y-1 text-[11px] leading-4'>
                <span>
                  {t('Cumulative eligible recharge')}:{' '}
                  {formatPaidCredits(info?.paid_credits)}
                </span>
                {info?.credits_to_next_level != null && (
                  <span>
                    {t('{{amount}} needed for L{{level}}', {
                      amount: formatPaidCredits(
                        info?.credits_to_next_level,
                        true
                      ),
                      level: nextLevel,
                    })}
                  </span>
                )}
              </div>
            </div>
          ) : automaticLevel === 4 && recoveryLevel === null ? (
            <p className='text-muted-foreground mt-6 text-sm'>
              {t('Highest automatic level reached')}
            </p>
          ) : null}

          {recoveryLevel !== null ? (
            <div className='mt-4 space-y-2' data-testid='trust-level-recovery'>
              <p className='text-muted-foreground text-sm'>
                {t('Use the API to restore your automatic level L{{level}}.', {
                  level: recoveryLevel,
                })}
              </p>
              <Button
                variant='outline'
                size='sm'
                render={<a href='/getting-started' />}
              >
                {t('Continue setup')}
              </Button>
            </div>
          ) : null}

          <div className='mt-6 grid grid-cols-1 gap-3 sm:grid-cols-3'>
            <div className='bg-muted/40 rounded-md p-3'>
              <div className='text-muted-foreground flex items-center gap-2 text-xs'>
                <HugeiconsIcon
                  icon={ChartIncreaseIcon}
                  className='size-4'
                  aria-hidden='true'
                />
                {t('Automatic recharge level')}
              </div>
              <p className='mt-2 font-mono text-lg font-semibold'>
                {automaticLevel != null &&
                automaticLevel >= 0 &&
                automaticLevel <= 4
                  ? `L${automaticLevel}`
                  : '-'}
              </p>
            </div>
            <div className='bg-muted/40 rounded-md p-3'>
              <div className='text-muted-foreground flex items-center gap-2 text-xs'>
                <HugeiconsIcon
                  icon={DiscountTag01Icon}
                  className='size-4'
                  aria-hidden='true'
                />
                {t('Current discount')}
              </div>
              <p className='mt-2 font-mono text-lg font-semibold'>
                {formatDiscount(info?.discount_percent ?? 0)}
              </p>
            </div>
            <div className='bg-muted/40 rounded-md p-3'>
              <div className='text-muted-foreground flex items-center gap-2 text-xs'>
                <HugeiconsIcon
                  icon={Clock01Icon}
                  className='size-4'
                  aria-hidden='true'
                />
                {t(roleAssigned ? 'Assignment' : 'Activity review')}
              </div>
              <p className='text-muted-foreground mt-2 text-xs leading-5'>
                {decayLabel}
              </p>
            </div>
          </div>
        </div>

        <div className='min-w-0'>
          <div className='flex items-center justify-between gap-3'>
            <div>
              <p className='text-sm font-semibold'>
                {t('Automatic recharge levels (L0–L4)')}
              </p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Each level has its configured benefits and usage discount.'
                )}
              </p>
            </div>
            {!roleAssigned && (info?.decay_period_days ?? 0) > 0 && (
              <Badge variant='secondary'>
                {t('{{days}}-day review', {
                  days: info?.decay_period_days ?? 90,
                })}
              </Badge>
            )}
          </div>
          <Separator className='my-4' />
          <div className='grid grid-cols-5 gap-1.5 sm:gap-2'>
            {automaticTiers.map((tier: TrustLevelTier) => {
              const active = tier.level === currentLevel
              const automatic = tier.level === automaticLevel
              const benefitSummary = formatTierBenefits(tier, t)
              return (
                <div
                  key={tier.level}
                  className={`min-w-0 rounded-md border p-2 text-center transition-colors ${
                    active ? 'border-primary bg-primary/10' : 'bg-muted/20'
                  }`}
                >
                  <div className='flex items-center justify-center gap-1'>
                    <span className='font-mono text-sm font-semibold'>
                      L{tier.level}
                    </span>
                    {automatic && !active && (
                      <span
                        className='bg-muted size-1.5 rounded-full'
                        aria-label={t('Automatic level')}
                      />
                    )}
                  </div>
                  <p className='mt-2 font-mono text-xs font-medium'>
                    {tier.discount_hidden
                      ? '?'
                      : formatDiscount(tier.discount_percent)}
                  </p>
                  <p className='text-muted-foreground mt-1 truncate text-[10px]'>
                    {tier.min_paid_credits === '0'
                      ? t('No minimum')
                      : formatPaidCredits(tier.min_paid_credits, true)}
                  </p>
                  <p
                    className='text-muted-foreground mt-2 line-clamp-2 text-[10px] leading-4'
                    title={benefitSummary}
                  >
                    {benefitSummary}
                  </p>
                </div>
              )
            })}
          </div>
          <div
            className='mt-4 grid gap-2 sm:grid-cols-2'
            data-testid='role-levels'
          >
            {([5, 6] as const).map((level) => (
              <div
                key={level}
                className={`flex min-w-0 items-start gap-3 rounded-md border p-3 ${currentLevel === level ? 'border-primary bg-primary/10' : 'bg-muted/20'}`}
              >
                <span className='font-mono text-sm font-semibold'>
                  L{level}
                </span>
                <div className='min-w-0 text-xs'>
                  <p className='font-medium'>
                    {t(level === 5 ? 'Administrator' : 'Super administrator')}
                  </p>
                  <p className='text-muted-foreground mt-1'>
                    {t('Assigned by account role')}
                  </p>
                  {user?.trust_level_role_tiers
                    ?.filter((tier) => tier.level === level)
                    .map((tier) => (
                      <div
                        key={tier.level}
                        className='text-muted-foreground mt-2 space-y-1'
                      >
                        <p>
                          {formatDiscount(tier.discount_percent)}{' '}
                          {t('usage discount')}
                        </p>
                        <p>
                          {tier.benefits
                            .map((code) => benefitLabel(code, t))
                            .join(' · ') || t('No additional benefits')}
                        </p>
                      </div>
                    ))}
                </div>
              </div>
            ))}
          </div>
          <p className='text-muted-foreground mt-4 text-xs leading-5'>
            {t(
              'Only successful external top-ups count. Long periods without API activity can reduce automatic levels.'
            )}
            <span className='mt-1 block'>
              {t(
                'This is the amount credited to your API balance, not the amount charged. LinuxDO Credit payments are excluded.'
              )}
            </span>
          </p>
        </div>
      </div>
    </section>
  )
}
