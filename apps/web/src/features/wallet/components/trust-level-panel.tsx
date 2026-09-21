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
import { Progress } from '@/components/ui/progress'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { formatTimestampToDate } from '@/lib/format'
import type { TrustLevelTier } from '@/stores/auth-store'

import { formatPlatformCreditBalance as formatPlatformCreditBalanceBase } from '../lib'
import type { UserWalletData } from '../types'

interface TrustLevelPanelProps {
  user: UserWalletData | null
  loading?: boolean
}

function formatDiscount(percent: number) {
  return `${Math.round(percent)}%`
}

function benefitLabel(code: string, t: (key: string) => string) {
  switch (code) {
    case 'developer_access':
      return t('Developer console access')
    case 'usage_discount':
      return t('Usage discount')
    case 'standard_access':
      return t('Standard access')
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
}: TrustLevelPanelProps) {
  const { t } = useTranslation()
  const formatPlatformCreditBalance = (amount: number) =>
    formatPlatformCreditBalanceBase(amount, t('Platform'))
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

  const currentLevel = info?.level ?? 0
  const automaticLevel = info?.automatic_level ?? currentLevel
  const nextTier = tiers.find((tier) => tier.level === info?.next_level)
  const currentTier = tiers.find((tier) => tier.level === automaticLevel)
  const previousAmount = currentTier?.min_paid_amount ?? 0
  const nextAmount = nextTier?.min_paid_amount ?? previousAmount
  const amountRange = Math.max(nextAmount - previousAmount, 1)
  const creditedAmountUSD = info?.paid_amount ?? 0
  const progress = info?.next_level
    ? Math.min(
        100,
        Math.max(0, ((creditedAmountUSD - previousAmount) / amountRange) * 100)
      )
    : 100
  const roleAssigned = currentLevel >= 5
  let decayLabel = t('No further decay at the current level')
  if (info?.overridden) {
    decayLabel = t('Paused while an administrator override is active')
  } else if (info?.next_decay_at) {
    decayLabel = t('Next review {{date}}', {
      date: formatTimestampToDate(info.next_decay_at),
    })
  }
  let statusLabel = t('Automatic')
  if (info?.overridden) {
    statusLabel = t('Administrator override')
  } else if (roleAssigned) {
    statusLabel = t('Role-assigned access')
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
                <p className='text-sm font-semibold'>{t('Trust program')}</p>
                <p className='text-muted-foreground text-xs'>
                  {roleAssigned
                    ? t('Role-assigned access')
                    : t('Benefits grow with account history')}
                </p>
              </div>
            </div>
            <Badge variant={info?.overridden ? 'warning' : 'outline'}>
              {statusLabel}
            </Badge>
          </div>

          <div className='mt-6 flex items-end gap-3'>
            <span className='font-mono text-5xl leading-none font-semibold tracking-tight tabular-nums'>
              L{currentLevel}
            </span>
            <span className='text-muted-foreground mb-1 text-sm'>
              {formatDiscount(info?.discount_percent ?? 0)}{' '}
              {t('usage discount')}
            </span>
          </div>

          <div className='mt-6 space-y-2'>
            <div className='flex items-center justify-between gap-3 text-xs'>
              <span className='text-muted-foreground'>
                {info?.next_level
                  ? t('Progress to L{{level}}', { level: info.next_level })
                  : t('Highest automatic level reached')}
              </span>
              <span className='font-medium tabular-nums'>
                {Math.round(progress)}%
              </span>
            </div>
            <Progress value={progress} className='h-2' />
            <div className='text-muted-foreground flex flex-wrap justify-between gap-x-4 gap-y-1 text-[11px] leading-4'>
              <span>
                {t('Eligible credited amount (USD)')}:{' '}
                {formatPlatformCreditBalance(creditedAmountUSD)}
              </span>
              {info?.amount_to_next_level != null && info.next_level && (
                <span>
                  {t('{{amount}} credited USD needed for L{{level}}', {
                    amount: formatPlatformCreditBalance(
                      info.amount_to_next_level
                    ),
                    level: info.next_level,
                  })}
                </span>
              )}
            </div>
          </div>

          <div className='mt-6 grid grid-cols-1 gap-3 sm:grid-cols-3'>
            <div className='bg-muted/40 rounded-md p-3'>
              <div className='text-muted-foreground flex items-center gap-2 text-xs'>
                <HugeiconsIcon
                  icon={ChartIncreaseIcon}
                  className='size-4'
                  aria-hidden='true'
                />
                {t(roleAssigned ? 'Trust level' : 'Automatic level')}
              </div>
              <p className='mt-2 font-mono text-lg font-semibold'>
                L{automaticLevel}
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
                {t('Activity review')}
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
              <p className='text-sm font-semibold'>{t('Level benefits')}</p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t('Higher levels reduce usage cost')}
              </p>
            </div>
            <Badge variant='secondary'>
              {t('{{days}}-day review', {
                days: info?.decay_period_days ?? 90,
              })}
            </Badge>
          </div>
          <Separator className='my-4' />
          <div className='grid grid-cols-5 gap-1.5 sm:gap-2'>
            {tiers.map((tier: TrustLevelTier) => {
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
                    {tier.min_paid_amount === 0
                      ? t('No minimum')
                      : formatPlatformCreditBalance(tier.min_paid_amount)}
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
