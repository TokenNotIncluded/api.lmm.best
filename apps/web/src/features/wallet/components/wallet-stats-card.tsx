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
import { Activity, BarChart3, ShieldCheck, WalletCards } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import type { UserWalletData } from '../types'
import { PlatformCreditAmount } from './platform-credit-help'
import { WalletTokenCloud, type WalletCloudSuccess } from './wallet-token-cloud'

function formatDiscount(percent: number) {
  return `${Math.round(percent)}%`
}

interface WalletStatsCardProps {
  user: UserWalletData | null
  loading?: boolean
  success?: WalletCloudSuccess | null
  onSuccessComplete?: (orderId: number) => void
}

export function WalletStatsCard(props: WalletStatsCardProps) {
  const { t } = useTranslation()
  const configuredQuotaPerUnit = useSystemConfigStore(
    (state) => state.config.currency.quotaPerUnit
  )
  const quotaPerUnit =
    Number.isFinite(configuredQuotaPerUnit) && configuredQuotaPerUnit > 0
      ? configuredQuotaPerUnit
      : DEFAULT_CURRENCY_CONFIG.quotaPerUnit
  const balanceCredits = Math.max(0, (props.user?.quota ?? 0) / quotaPerUnit)
  if (props.loading) {
    return (
      <div className='bg-card grid grid-cols-3 overflow-hidden rounded-xl border'>
        {['balance', 'usage', 'requests', 'trust'].map((key, index) => (
          <div
            key={key}
            className={cn(
              'min-w-0 p-3.5 sm:px-6 sm:py-4',
              index === 0
                ? 'bg-primary/5 col-span-3 min-h-32 border-b sm:min-h-36'
                : 'border-e last:border-e-0'
            )}
          >
            <Skeleton className='h-3.5 w-full' />
            <Skeleton className='mt-3 h-6 w-3/4' />
            <Skeleton className='mt-2 hidden h-3 w-24 md:block' />
          </div>
        ))}
      </div>
    )
  }

  const stats: {
    label: string
    value: string
    description: string
    icon: typeof WalletCards
    tone: IconBadgeTone
  }[] = [
    {
      label: t('Current Balance'),
      value: formatQuota(props.user?.quota ?? 0),
      description: t('Remaining quota'),
      icon: WalletCards,
      tone: 'primary',
    },
    {
      label: t('Total Usage'),
      value: formatQuota(props.user?.used_quota ?? 0),
      description: t('Total consumed quota'),
      icon: BarChart3,
      tone: 'neutral',
    },
    {
      label: t('API Requests'),
      value: (props.user?.request_count ?? 0).toLocaleString(),
      description: t('Total requests made'),
      icon: Activity,
      tone: 'neutral',
    },
    {
      label: t('Trust level'),
      value: `L${props.user?.trust_level_info?.level ?? 0}`,
      description: `${formatDiscount(props.user?.trust_level_info?.discount_percent ?? 0)} ${t('discount')}`,
      icon: ShieldCheck,
      tone: 'neutral',
    },
  ]

  return (
    <div className='bg-card grid grid-cols-3 overflow-hidden rounded-xl border'>
      {stats.map((item, index) => (
        <div
          key={item.label}
          className={cn(
            'min-w-0 p-3.5 sm:px-6 sm:py-4',
            index === 0
              ? 'bg-primary/5 relative isolate col-span-3 min-h-32 overflow-hidden border-b sm:min-h-36'
              : 'border-e last:border-e-0'
          )}
        >
          {index === 0 && (
            <WalletTokenCloud
              amount={balanceCredits}
              success={props.success}
              onSuccessComplete={props.onSuccessComplete}
            />
          )}
          <div className='pointer-events-none relative z-10 flex items-center gap-2'>
            <IconBadge tone={item.tone} size='stat'>
              <item.icon />
            </IconBadge>
            <div className='text-muted-foreground min-h-8 text-xs leading-4 font-medium sm:min-h-0'>
              {item.label}
            </div>
          </div>

          <div
            className={cn(
              'text-foreground pointer-events-none relative z-10 mt-2.5 font-semibold tracking-tight break-words tabular-nums',
              index === 0 ? 'text-3xl sm:text-4xl' : 'text-sm sm:text-xl'
            )}
          >
            {index < 2 ? (
              <PlatformCreditAmount value={item.value} />
            ) : (
              item.value
            )}
          </div>
          <div
            className={cn(
              'text-muted-foreground pointer-events-none relative z-10 mt-1 text-xs',
              index > 0 && 'hidden sm:block'
            )}
          >
            {item.description}
          </div>
        </div>
      ))}
    </div>
  )
}
