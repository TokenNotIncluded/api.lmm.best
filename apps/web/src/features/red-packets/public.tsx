import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Gift, RotateCcw, Ticket, Wallet } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
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
    <div className='bg-background/80 rounded-xl border p-4 shadow-sm backdrop-blur'>
      <div className='flex items-start gap-3'>
        <div className='bg-primary/10 text-primary flex size-10 shrink-0 items-center justify-center rounded-full'>
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
            <code className='bg-muted min-w-0 flex-1 truncate rounded-md px-3 py-2 text-xs'>
              {reward.code}
            </code>
            <Button
              size='sm'
              variant='outline'
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
    return <div className='text-muted-foreground flex min-h-screen items-center justify-center'>{t('Loading...')}</div>
  }
  if (!packet) {
    return <div className='text-muted-foreground flex min-h-screen items-center justify-center'>{t('This red packet is unavailable.')}</div>
  }

  const now = Math.floor(Date.now() / 1000)
  const inactive =
    !packet.enabled ||
    (packet.start_at > 0 && now < packet.start_at) ||
    (packet.end_at > 0 && now >= packet.end_at) ||
    packet.remaining_items <= 0

  return (
    <main className='bg-muted/25 min-h-screen px-4 py-10 sm:py-16'>
      <div className='mx-auto w-full max-w-xl'>
        <div className='bg-card overflow-hidden rounded-3xl border shadow-xl shadow-black/5'>
          {packet.cover_image ? (
            <img src={packet.cover_image} alt='' className='aspect-[3/1] w-full object-cover' />
          ) : (
            <div className='from-primary/20 via-primary/5 to-muted flex aspect-[3/1] items-center justify-center bg-gradient-to-br'>
              <Gift className='text-primary size-12' />
            </div>
          )}
          <div className='p-6 sm:p-8'>
            <div className='text-center'>
              <div className='bg-primary/10 text-primary mx-auto mb-4 flex size-12 items-center justify-center rounded-full'>
                <Gift className='size-6' />
              </div>
              <h1 className='text-2xl font-semibold tracking-tight'>{packet.title}</h1>
              {packet.description ? (
                <p className='text-muted-foreground mt-2 text-sm'>{packet.description}</p>
              ) : null}
              <p className='text-muted-foreground mt-3 text-xs'>
                {packet.remaining_items}/{packet.total_items} {t('remaining')} · {packet.claim_count} {t('claimed')}
              </p>
            </div>

            <div className='mt-6'>
              {!user ? (
                <Button className='w-full' size='lg' render={<Link to='/sign-in' search={{ redirect: `/red-packet/${slug}` }} />}>
                  {t('Sign in to draw')}
                </Button>
              ) : (
                <Button
                  className='w-full'
                  size='lg'
                  disabled={inactive || remainingDraws <= 0 || claimMutation.isPending}
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
                  {t('{{count}} draw(s) remaining for you', { count: remainingDraws })}
                </div>
              ) : null}
            </div>
          </div>
        </div>

        {claims.length > 0 ? (
          <section className='mt-6 space-y-3'>
            <div className='flex items-center gap-2 px-1 text-sm font-medium'>
              <Check className='size-4' /> {t('Your rewards')}
            </div>
            {claims.map((reward) => (
              <RewardCard key={reward.claim_id} reward={reward} />
            ))}
          </section>
        ) : null}
      </div>
    </main>
  )
}
