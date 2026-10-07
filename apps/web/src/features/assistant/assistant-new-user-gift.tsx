/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { CheckmarkCircle02Icon, GiftIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'

import {
  AssistantRequestError,
  claimAssistantNewUserGift,
  getAssistantNewUserGift,
} from './api'

export function AssistantNewUserGift(props: { enabled: boolean }) {
  const { t } = useTranslation()
  const { formatQuota } = useWalletCurrency()
  const queryClient = useQueryClient()
  const [claiming, setClaiming] = useState(false)
  const giftQuery = useQuery({
    queryKey: ['assistant-new-user-gift'],
    queryFn: getAssistantNewUserGift,
    enabled: props.enabled,
    staleTime: 15_000,
    retry: false,
  })
  const gift = giftQuery.data
  if (!props.enabled) return null

  if (giftQuery.isError && !gift) {
    return (
      <section
        className='border-border/60 my-2 flex flex-wrap items-center justify-between gap-3 border-y px-1 py-4 sm:px-4'
        data-testid='assistant-new-user-gift-error'
      >
        <p className='text-muted-foreground text-sm'>{t('Failed to load')}</p>
        <Button
          type='button'
          size='sm'
          variant='outline'
          onClick={() => void giftQuery.refetch()}
        >
          {t('Retry')}
        </Button>
      </section>
    )
  }

  if (!gift) return null

  const giftTitle =
    gift.status === 'declined' ? t('No gift this time') : t('New-user gift')

  const claim = async () => {
    if (
      claiming ||
      gift.status !== 'offered' ||
      gift.claim_available === false
    ) {
      return
    }
    setClaiming(true)
    try {
      const result = await claimAssistantNewUserGift()
      queryClient.setQueryData(['assistant-new-user-gift'], result.gift)
      await queryClient.invalidateQueries({ queryKey: ['assistant-status'] })
      toast.success(t('Welcome gift claimed'))
    } catch (error) {
      toast.error(
        error instanceof AssistantRequestError && error.code === 'gift_disabled'
          ? t('New-user gifts are currently disabled.')
          : error instanceof AssistantRequestError &&
              error.code === 'gift_limit_exceeded'
            ? t('This gift exceeds the current maximum and cannot be claimed.')
            : error instanceof Error
              ? error.message
              : t('Unable to claim welcome gift')
      )
      await giftQuery.refetch()
    } finally {
      setClaiming(false)
    }
  }

  let giftAction: ReactNode = (
    <span className='text-muted-foreground text-xs'>
      {t('Opportunity used')}
    </span>
  )
  if (gift.status === 'offered') {
    giftAction = (
      <Button
        type='button'
        size='sm'
        onClick={() => void claim()}
        disabled={claiming || gift.claim_available === false}
      >
        {claiming ? t('Claiming...') : t('Claim gift')}
      </Button>
    )
  } else if (gift.status === 'claimed') {
    giftAction = <span className='text-success text-xs'>{t('Claimed')}</span>
  }

  return (
    <section
      className='border-border/60 bg-accent/20 my-2 grid gap-3 border-y px-1 py-5 sm:grid-cols-[1fr_auto] sm:items-center sm:px-4'
      data-testid='assistant-new-user-gift'
    >
      <div className='flex min-w-0 gap-3'>
        <HugeiconsIcon
          icon={gift.status === 'claimed' ? CheckmarkCircle02Icon : GiftIcon}
          className={
            gift.status === 'claimed'
              ? 'text-success mt-0.5 size-5'
              : 'mt-0.5 size-5'
          }
          strokeWidth={2}
          aria-hidden='true'
        />
        <div className='min-w-0'>
          <p className='text-sm font-medium'>
            {giftTitle} ·{' '}
            {formatQuota(gift.credit_amount ?? gift.quota, {
              abbreviate: false,
              digitsLarge: 2,
              digitsSmall: 2,
            })}
          </p>
          {gift.status === 'offered' && gift.claim_available === false && (
            <p className='text-muted-foreground mt-1 text-xs leading-5'>
              {t(
                gift.claim_blocked_code === 'gift_disabled'
                  ? 'New-user gifts are currently disabled.'
                  : 'This gift exceeds the current maximum and cannot be claimed.'
              )}
            </p>
          )}
          <p className='text-muted-foreground mt-1 text-xs leading-5'>
            {gift.reason}
          </p>
        </div>
      </div>
      {giftAction}
    </section>
  )
}
