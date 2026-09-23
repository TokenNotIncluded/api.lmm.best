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
import { Check, CircleAlert, RotateCcw, Ticket, Wallet } from 'lucide-react'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { formatQuota } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { claimRedPacket, getMyRedPacketClaims, getRedPacket } from './api'
import type { RedPacketReward } from './types'

/**
 * The seal that is torn open. Two halves separate along the centre seam while
 * the whole packet presses inwards, so the draw feels like unwrapping paper
 * rather than a button that swaps text.
 */
function RedPacketSeal({
  label,
  pending,
  disabled,
  onOpen,
}: {
  label: string
  pending: boolean
  disabled: boolean
  onOpen: () => void
}) {
  const { t } = useTranslation()
  const shouldReduce = useReducedMotion()
  const [burst, setBurst] = useState(0)
  return (
    <button
      type='button'
      onClick={() => {
        if (pending || disabled) return
        setBurst((value) => value + 1)
        onOpen()
      }}
      disabled={pending || disabled}
      aria-label={label}
      className='group bg-primary text-primary-foreground border-primary focus-visible:outline-ring relative block min-h-[13rem] w-full overflow-hidden rounded-xl border p-0 text-left focus-visible:outline-2 focus-visible:outline-offset-4 disabled:cursor-not-allowed disabled:opacity-60 motion-safe:transition-transform motion-safe:duration-200 motion-safe:active:scale-[0.97]'
      style={{ perspective: 900 }}
    >
      {/* Gold rule that peels away with the top half. */}
      <span
        aria-hidden='true'
        className='bg-primary-foreground/20 absolute inset-x-6 top-1/2 block h-px -translate-y-1/2'
      />
      {[0, 1].map((half) => (
        <motion.span
          key={half}
          aria-hidden='true'
          className='pointer-events-none absolute inset-0 block overflow-hidden rounded-xl'
          style={{
            clipPath: half === 0 ? 'inset(0 0 50% 0)' : 'inset(50% 0 0 0)',
            transformOrigin: half === 0 ? '50% 100%' : '50% 0%',
          }}
          animate={
            shouldReduce || burst === 0
              ? { y: 0, rotateX: 0, opacity: 1 }
              : burst > 0 && pending
                ? {
                    y: half === 0 ? -14 : 14,
                    rotateX: half === 0 ? -34 : 34,
                    opacity: 0.55,
                  }
                : { y: 0, rotateX: 0, opacity: 1 }
          }
          transition={{ duration: shouldReduce ? 0 : 0.3, ease: 'easeOut' }}
        >
          <span className='bg-primary-foreground/5 absolute inset-0' />
        </motion.span>
      ))}
      <span className='relative flex min-h-[13rem] flex-col items-center justify-center gap-3 px-6 py-10 text-center'>
        <span
          aria-hidden='true'
          className='bg-background text-foreground relative grid size-16 place-items-center rounded-full'
        >
          <Ticket className='size-7' />
        </span>
        <span className='font-serif text-2xl sm:text-3xl'>
          {pending ? t('Drawing...') : label}
        </span>
        {!disabled && (
          <span className='text-xs opacity-80'>
            {t('Tap the seal to unwrap')}
          </span>
        )}
      </span>
      <AnimatePresence>
        {pending && !shouldReduce ? (
          <motion.span
            key={burst}
            aria-hidden='true'
            className='pointer-events-none absolute inset-0 block'
          >
            {Array.from({ length: 12 }).map((_, index) => {
              const angle = (index / 12) * Math.PI * 2
              return (
                <motion.span
                  key={index}
                  className='bg-primary-foreground absolute top-1/2 left-1/2 size-1.5 rounded-full'
                  initial={{ x: 0, y: 0, opacity: 1, scale: 1 }}
                  animate={{
                    x: Math.cos(angle) * 110,
                    y: Math.sin(angle) * 80,
                    opacity: 0,
                    scale: 0.4,
                  }}
                  transition={{ duration: 0.4, ease: 'easeOut' }}
                />
              )
            })}
          </motion.span>
        ) : null}
      </AnimatePresence>
    </button>
  )
}

function RewardCard({
  reward,
  index,
}: {
  reward: RedPacketReward
  index: number
}) {
  const { t } = useTranslation()
  const shouldReduce = useReducedMotion()
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
    <motion.div
      initial={shouldReduce ? false : { opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{
        duration: shouldReduce ? 0 : 0.3,
        delay: shouldReduce ? 0 : Math.min(index, 6) * 0.04,
      }}
      className='border-foreground/20 border-b py-5'
    >
      <div className='flex items-start gap-3'>
        <div className='bg-muted text-foreground flex size-10 shrink-0 items-center justify-center rounded-md'>
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
          <div className='mt-3 flex items-stretch gap-2'>
            <code className='bg-muted min-w-0 flex-1 truncate rounded-md px-3 py-2 text-xs leading-6'>
              {reward.code}
            </code>
            <CopyButton
              value={reward.code}
              variant='outline'
              aria-label={t('Copy')}
              className='h-auto min-h-11 px-3'
            />
          </div>
        </div>
      </div>
    </motion.div>
  )
}

/** Just-won reward shown inside the torn-open packet. */
function UnwrappedReward({ reward }: { reward: RedPacketReward }) {
  const { t } = useTranslation()
  const shouldReduce = useReducedMotion()
  const isDiscount = reward.item_type === 'discount'
  const isResetVoucher = reward.reward_type === 'reset_voucher'
  const headline = isDiscount
    ? t('{{percent}}% discount code', { percent: reward.discount_percent ?? 0 })
    : isResetVoucher
      ? t('Banked reset voucher for plan #{{plan}}', {
          plan: reward.reset_plan_id ?? 0,
        })
      : reward.quota !== undefined
        ? formatQuota(reward.quota)
        : reward.name

  return (
    <motion.section
      initial={shouldReduce ? false : { opacity: 0, scale: 0.94, y: 16 }}
      animate={{ opacity: 1, scale: 1, y: 0 }}
      transition={{ duration: shouldReduce ? 0 : 0.3, ease: 'easeOut' }}
      className='bg-card relative mt-8 overflow-hidden rounded-xl border p-6'
      aria-live='polite'
    >
      <p className='text-muted-foreground text-xs tracking-wide uppercase'>
        {t('You unwrapped')}
      </p>
      <p className='mt-2 font-serif text-3xl leading-tight sm:text-4xl'>
        {headline}
      </p>
      <p className='text-muted-foreground mt-2 text-sm'>
        {isDiscount
          ? t('This discount code is bound to your account after claiming.')
          : t('Redeem this code in Wallet when you are ready.')}
      </p>
      <div className='mt-4 flex items-stretch gap-2'>
        <code className='bg-background/70 min-w-0 flex-1 truncate rounded-md border px-3 py-2 text-sm leading-6'>
          {reward.code}
        </code>
        <CopyButton
          value={reward.code}
          variant='outline'
          aria-label={t('Copy')}
          className='h-auto min-h-11 px-3'
        />
      </div>
    </motion.section>
  )
}

export function RedPacketPublicPage({ slug }: { slug: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const shouldReduce = useReducedMotion()
  const [unwrapped, setUnwrapped] = useState<RedPacketReward | null>(null)
  const [claimError, setClaimError] = useState<string | null>(null)

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
    onMutate: () => setClaimError(null),
    onError: () => setClaimError(t('Unable to claim red packet')),
    onSuccess: async (response) => {
      if (!response.success || !response.data) {
        setClaimError(response.message || t('Unable to claim red packet'))
        return
      }
      setUnwrapped(response.data)
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
  const claimedCount = Math.max(0, packet.total_items - packet.remaining_items)
  const progress =
    packet.total_items > 0 ? Math.min(1, claimedCount / packet.total_items) : 0
  const openLabel =
    claimsQuery.isPending && user
      ? t('Loading...')
      : remainingDraws <= 0
        ? t('You have used all draws')
        : inactive
          ? t('Red packet unavailable')
          : t('Open red packet')

  return (
    <ForgePublicShell>
      <main className='mx-auto min-h-svh w-full max-w-5xl px-5 pt-12 pb-20 md:px-10 md:pt-16'>
        <div className='mx-auto w-full max-w-3xl'>
          <div className='border-foreground/20 border-y'>
            {packet.cover_image ? (
              <div className='relative'>
                <img
                  src={packet.cover_image}
                  alt=''
                  className='aspect-[3/1] w-full object-cover'
                />
                <div
                  aria-hidden='true'
                  className='bg-foreground/10 absolute inset-0'
                />
              </div>
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
                <p className='text-muted-foreground mt-5 text-sm tabular-nums'>
                  {packet.remaining_items}/{packet.total_items} {t('remaining')}{' '}
                  · {packet.claim_count} {t('claimed')}
                </p>
                <div
                  className='bg-foreground/10 mt-3 h-1.5 w-full max-w-sm overflow-hidden rounded-full'
                  role='progressbar'
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={Math.round(progress * 100)}
                  aria-label={t('Claimed')}
                >
                  <motion.div
                    className='bg-primary h-full origin-left rounded-full'
                    initial={false}
                    animate={{ scaleX: progress }}
                    transition={{
                      duration: shouldReduce ? 0 : 0.3,
                      ease: 'easeOut',
                    }}
                  />
                </div>
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
                  <div className='space-y-3'>
                    <RedPacketSeal
                      label={openLabel}
                      pending={claimMutation.isPending}
                      disabled={
                        inactive ||
                        remainingDraws <= 0 ||
                        claimsQuery.isPending ||
                        claimsQuery.isError
                      }
                      onOpen={() => claimMutation.mutate()}
                    />
                    {claimsQuery.isError ? (
                      <div
                        className='text-destructive space-y-2 text-sm'
                        role='alert'
                      >
                        <p className='flex items-center gap-2'>
                          <CircleAlert className='size-4 shrink-0' />
                          {t('Request failed')}
                        </p>
                        <Button
                          variant='outline'
                          onClick={() => void claimsQuery.refetch()}
                        >
                          {t('Retry')}
                        </Button>
                      </div>
                    ) : null}
                    {claimError ? (
                      <p
                        className='text-destructive flex items-start gap-2 text-sm'
                        role='alert'
                      >
                        <CircleAlert className='mt-0.5 size-4 shrink-0' />
                        {claimError}
                      </p>
                    ) : null}
                    {remainingDraws > 0 && !inactive ? (
                      <p className='text-muted-foreground text-center text-xs'>
                        {t('{{count}} draw(s) remaining for you', {
                          count: remainingDraws,
                        })}
                      </p>
                    ) : null}
                  </div>
                )}
                {!inactive &&
                packet.remaining_items > 0 &&
                packet.draw_mode !== 'sequence' ? (
                  <p className='text-muted-foreground mt-4 text-center text-xs'>
                    {t('Every draw is random. Nobody can pick their reward.')}
                  </p>
                ) : null}
                <Button
                  variant='ghost'
                  size='sm'
                  className='mt-4 w-full'
                  render={<Link to='/pricing' />}
                >
                  {t('See what credits can buy')}
                </Button>
              </div>
            </div>
          </div>

          <AnimatePresence>
            {unwrapped ? (
              <UnwrappedReward key='unwrapped' reward={unwrapped} />
            ) : null}
          </AnimatePresence>

          {claims.length > 0 ? (
            <section className='mt-10'>
              <div className='border-foreground/20 flex items-center gap-2 border-b pb-4 text-sm font-semibold'>
                <Check className='size-4' /> {t('Your rewards')}
              </div>
              {claims.map((reward, index) => (
                <RewardCard
                  key={reward.claim_id}
                  reward={reward}
                  index={index}
                />
              ))}
            </section>
          ) : null}
        </div>
      </main>
    </ForgePublicShell>
  )
}
