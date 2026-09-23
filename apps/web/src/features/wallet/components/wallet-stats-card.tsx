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

import type { UserWalletData } from '../types'

function formatDiscount(percent: number) {
  return `${Math.round(percent)}%`
}

interface WalletStatsCardProps {
  user: UserWalletData | null
  loading?: boolean
}

export function WalletStatsCard(props: WalletStatsCardProps) {
  const { t } = useTranslation()
  if (props.loading) {
    return (
      <div className='bg-card grid grid-cols-2 overflow-hidden rounded-xl border sm:grid-cols-[1.25fr_repeat(3,minmax(0,1fr))]'>
        {['balance', 'usage', 'requests', 'trust'].map((key, index) => (
          <div
            key={key}
            className={cn(
              'min-w-0 border-b p-3.5 even:border-l sm:border-b-0 sm:border-l sm:px-5 sm:py-4 sm:first:border-l-0',
              index > 1 && 'border-b-0',
              index === 0 && 'bg-primary/5'
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
    <div className='bg-card grid grid-cols-2 overflow-hidden rounded-xl border sm:grid-cols-[1.25fr_repeat(3,minmax(0,1fr))]'>
      {stats.map((item, index) => (
        <div
          key={item.label}
          className={cn(
            'min-w-0 p-3.5 sm:px-5 sm:py-4',
            'border-b even:border-l sm:border-b-0 sm:border-l sm:first:border-l-0',
            index > 1 && 'border-b-0',
            index === 0 && 'bg-primary/5'
          )}
        >
          <div className='flex items-center gap-2'>
            <IconBadge tone={item.tone} size='stat'>
              <item.icon />
            </IconBadge>
            <div className='text-muted-foreground truncate text-xs font-medium'>
              {item.label}
            </div>
          </div>

          <div
            className={cn(
              'text-foreground mt-2.5 font-semibold tracking-tight break-words tabular-nums',
              index === 0 ? 'text-lg sm:text-2xl' : 'text-lg sm:text-xl'
            )}
          >
            {item.value}
          </div>
          <div className='text-muted-foreground mt-1 hidden text-xs md:block'>
            {item.description}
          </div>
        </div>
      ))}
    </div>
  )
}
