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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Check, Copy, RotateCcw, Ticket, Wallet } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { formatQuota } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { claimRedPacket, getMyRedPacketClaims, getRedPacket } from './api'
import type { RedPacketReward } from './types'

function RewardCard({ reward }: { reward: RedPacketReward }) {
  const { t } = useTranslation()
  const isDiscount = reward.item_type === 'discount'
  const isResetVoucher = reward.reward_type === 'reset_voucher'

  let description = reward.name
  if (isDiscount) {
    description = t('{{percent}}% discount code', {
      percent: reward.discount_percent ?? 0,
    })
  } else if (isResetVoucher) {
    description = t('Banked reset voucher for plan #{{plan}}', {
      plan: reward.reset_plan_id ?? 0,
    })
  } else if (reward.quota !== undefined) {
    description = t('Quota: {{quota}}', { quota: formatQuota(reward.quota) })
  }

  return (
    <div className='border-foreground/20 border-b py-5'>
      <div className='flex items-start gap-3'>
        <div className='bg-muted text-foreground flex size-10 shrink-0 items-center justify-center'>
          {isDiscount ? (
            <Ticket className='size-5' />
          ) : isResetVoucher ? (
            <RotateCcw className='size-5' />
          ) : (
            <Wallet className='size-5' />
          )}
        </div>
        <div className='min-w-0 flex-1'>
          <div className='font-medium'>{description}</div>
          <div className='text-muted-foreground mt-1 text-xs'>
            {isDiscount
              ? t('This discount code is bound to your account after claiming.')
              : t('Redeem this code in Wallet when you are ready.')}
          </div>
          <div className='mt-3 flex gap-2'>
            <code className='bg-muted min-w-0 flex-1 truncate px-3 py-2 text-xs'>
              {reward.code}
            </code>
            <Button
              size='sm'
              variant='outline'
              aria-label={t('Copy')}
              onClick={async () => {
                await navigator.clipboard?.writeText(reward.code)
                toast.success(t('Copied to clipboard'))
              }}
            >
              <Copy className='size-4' />
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}

export function RedPacketPublicPage({ slug }: { slug: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)

  const packetQuery = useQuery({
    queryKey: ['red-packet', slug],
    queryFn: () => getRedPacket(slug),
  })
  const claimsQuery = useQuery({
    queryKey: ['red-packet', slug, 'claims', user?.id],
    queryFn: () => getMyRedPacketClaims(slug),
    enabled: Boolean(user),
    retry: false,
  })
  const claimMutation = useMutation({
    mutationFn: () => claimRedPacket(slug),
    onSuccess: async (response) => {
      if (!response.success || !response.data) {
        toast.error(response.message || t('Unable to claim red packet'))
        return
      }
      toast.success(t('Claimed successfully'))
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['red-packet', slug] }),
        queryClient.invalidateQueries({
          queryKey: ['red-packet', slug, 'claims', user?.id],
        }),
      ])
    },
  })

  const packet = packetQuery.data?.data
  const claims = claimsQuery.data?.data ?? []
  const remainingDraws = packet
    ? Math.max(0, packet.per_user_limit - claims.length)
    : 0

  if (packetQuery.isLoading) {
    return (
      <ForgePublicShell>
        <main className='mx-auto flex min-h-[calc(100svh-4rem)] max-w-5xl items-center px-5 py-20 md:px-10'>
          <p className='text-muted-foreground' role='status'>
            {t('Loading...')}
          </p>
        </main>
      </ForgePublicShell>
    )
  }
  if (packetQuery.isError || !packet) {
    return (
      <ForgePublicShell>
        <main className='mx-auto flex min-h-[calc(100svh-4rem)] max-w-5xl flex-col justify-center px-5 py-20 md:px-10'>
          <h1 className='max-w-2xl font-serif text-5xl leading-[1.05] font-normal tracking-tight sm:text-6xl'>
            {packetQuery.isError
              ? t('Request failed')
              : t('This red packet is unavailable.')}
          </h1>
          {packetQuery.isError ? (
            <p className='text-muted-foreground mt-5 text-base leading-7'>
              {t('Please try again in a moment.')}
            </p>
          ) : null}
          <div className='mt-8 flex flex-wrap gap-3'>
            {packetQuery.isError ? (
              <Button onClick={() => void packetQuery.refetch()}>
                {t('Retry')}
              </Button>
            ) : null}
            <Button variant='outline' render={<Link to='/' />}>
              {t('Back to home')}
            </Button>
          </div>
        </main>
      </ForgePublicShell>
    )
  }

  const now = Math.floor(Date.now() / 1000)
  const inactive =
    !packet.enabled ||
    (packet.start_at > 0 && now < packet.start_at) ||
    (packet.end_at > 0 && now >= packet.end_at) ||
    packet.remaining_items <= 0

  return (
    <ForgePublicShell>
      <main className='mx-auto min-h-svh w-full max-w-5xl px-5 pt-12 pb-20 md:px-10 md:pt-16'>
        <div className='mx-auto w-full max-w-3xl'>
          <div className='border-foreground/20 border-y'>
            {packet.cover_image ? (
              <img
                src={packet.cover_image}
                alt=''
                className='aspect-[3/1] w-full object-cover'
              />
            ) : null}
            <div className='py-8'>
              <div>
                <h1 className='font-serif text-5xl leading-[1.05] font-normal tracking-tight sm:text-6xl'>
                  {packet.title}
                </h1>
                {packet.description ? (
                  <p className='text-muted-foreground mt-5 max-w-2xl text-base leading-7'>
                    {packet.description}
                  </p>
                ) : null}
                <p className='text-muted-foreground mt-5 text-sm'>
                  {packet.remaining_items}/{packet.total_items} {t('remaining')}{' '}
                  · {packet.claim_count} {t('claimed')}
                </p>
              </div>

              <div className='border-foreground/20 mt-8 border-t pt-8 sm:max-w-sm'>
                {!user ? (
                  <Button
                    className='min-h-11 w-full'
                    size='lg'
                    render={
                      <Link
                        to='/sign-in'
                        search={{ redirect: `/red-packet/${slug}` }}
                      />
                    }
                  >
                    {t('Sign in to draw')}
                  </Button>
                ) : (
                  <Button
                    className='min-h-11 w-full'
                    size='lg'
                    disabled={
                      inactive || remainingDraws <= 0 || claimMutation.isPending
                    }
                    onClick={() => claimMutation.mutate()}
                  >
                    {claimMutation.isPending
                      ? t('Drawing...')
                      : remainingDraws <= 0
                        ? t('You have used all draws')
                        : inactive
                          ? t('Red packet unavailable')
                          : t('Open red packet')}
                  </Button>
                )}
                {user && remainingDraws > 0 ? (
                  <div className='text-muted-foreground mt-2 text-center text-xs'>
                    {t('{{count}} draw(s) remaining for you', {
                      count: remainingDraws,
                    })}
                  </div>
                ) : null}
              </div>
            </div>
          </div>

          {claims.length > 0 ? (
            <section className='mt-10'>
              <div className='border-foreground/20 flex items-center gap-2 border-b pb-4 text-sm font-semibold'>
                <Check className='size-4' /> {t('Your rewards')}
              </div>
              {claims.map((reward) => (
                <RewardCard key={reward.claim_id} reward={reward} />
              ))}
            </section>
          ) : null}
        </div>
      </main>
    </ForgePublicShell>
  )
}
