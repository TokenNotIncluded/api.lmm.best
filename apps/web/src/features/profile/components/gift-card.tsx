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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Gift, CheckCircle2, Clock, Lock } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import dayjs from '@/lib/dayjs'

import { claimGift, getAvailableGifts } from '../api'
import type { GiftItem } from '../types'

/**
 * Compensation gift card shown on the profile page when the operator has
 * published time-boxed gifts. Users proactively claim within the window.
 */
export function GiftCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['available-gifts'],
    queryFn: async () => {
      const res = await getAvailableGifts()
      return res.success && Array.isArray(res.data) ? res.data : []
    },
    staleTime: 30_000,
  })

  const claimMutation = useMutation({
    mutationFn: (giftId: number) => claimGift(giftId),
    onSuccess: (res) => {
      if (res.success) {
        if (res.data?.already_claimed) {
          toast.info(t('You have already claimed this gift'))
        } else {
          toast.success(t('Gift claimed successfully'))
        }
        queryClient.invalidateQueries({ queryKey: ['available-gifts'] })
      } else {
        toast.error(res.message || t('Failed to claim gift'))
      }
    },
    onError: () => toast.error(t('Failed to claim gift')),
  })

  if (isLoading) {
    return (
      <Card className='p-4 sm:p-6'>
        <div className='flex items-center gap-3'>
          <Skeleton className='h-10 w-10 rounded-lg' />
          <div className='space-y-2'>
            <Skeleton className='h-4 w-32' />
            <Skeleton className='h-3 w-48' />
          </div>
        </div>
      </Card>
    )
  }

  const gifts = data ?? []
  if (gifts.length === 0) {
    return null
  }

  return (
    <Card className='p-4 sm:p-6'>
      <div className='mb-4 flex items-center gap-3'>
        <IconBadge tone='primary' size='lg' className='sm:size-11'>
          <Gift className='h-5 w-5' />
        </IconBadge>
        <div>
          <h3 className='text-foreground text-base font-semibold sm:text-lg'>
            {t('Compensation Gifts')}
          </h3>
          <p className='text-muted-foreground text-xs sm:text-sm'>
            {t('Claim time-limited compensation quota')}
          </p>
        </div>
      </div>

      <div className='space-y-3'>
        {gifts.map((gift) => (
          <GiftRow
            key={gift.id}
            gift={gift}
            claiming={claimMutation.isPending}
            onClaim={() => claimMutation.mutate(gift.id)}
          />
        ))}
      </div>
    </Card>
  )
}

function GiftRow({
  gift,
  claiming,
  onClaim,
}: {
  gift: GiftItem
  claiming: boolean
  onClaim: () => void
}) {
  const { t } = useTranslation()
  const currency = useWalletCurrency()
  const expired = gift.end_at * 1000 <= Date.now()

  return (
    <div className='border-border bg-card/50 flex flex-col gap-3 rounded-lg border p-3 sm:flex-row sm:items-center sm:justify-between'>
      <div className='min-w-0 flex-1 space-y-1'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-foreground text-sm font-medium'>
            {gift.title}
          </span>
          <Badge variant='secondary' className='text-xs'>
            {currency.formatQuota(gift.quota)}
          </Badge>
          {gift.claimed && (
            <Badge variant='outline' className='text-xs'>
              <CheckCircle2 className='mr-1 h-3 w-3' />
              {t('Claimed')}
            </Badge>
          )}
          {!gift.claimed && expired && (
            <Badge variant='outline' className='text-muted-foreground text-xs'>
              {t('Expired')}
            </Badge>
          )}
        </div>
        {gift.description && (
          <p className='text-muted-foreground line-clamp-2 text-xs'>
            {gift.description}
          </p>
        )}
        <div className='text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs'>
          <span className='inline-flex items-center gap-1'>
            <Clock className='h-3 w-3' />
            {t('Ends at {{time}}', {
              time: dayjs(gift.end_at * 1000).format('YYYY-MM-DD HH:mm'),
            })}
          </span>
          {!gift.eligible && !gift.claimed && gift.reason && (
            <span className='inline-flex items-center gap-1'>
              <Lock className='h-3 w-3' />
              {gift.reason}
            </span>
          )}
        </div>
      </div>

      <div className='shrink-0'>
        {gift.claimed ? (
          <Button size='sm' variant='outline' disabled>
            {t('Claimed')}
          </Button>
        ) : (
          <Button
            size='sm'
            disabled={claiming || !gift.eligible || expired}
            onClick={onClaim}
          >
            {claiming ? t('Claiming...') : t('Claim')}
          </Button>
        )}
      </div>
    </div>
  )
}
