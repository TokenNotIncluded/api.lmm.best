/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { getUserAvatarFallback, getUserAvatarStyle } from '@/lib/avatar'

import { storeSellerId, storeSellerMailto } from './merchant-profile'
import type { StoreSeller } from './types'

export function StoreMerchantIdentity({
  seller,
  sellerId,
  compact = false,
}: {
  seller?: StoreSeller | null
  sellerId?: number
  compact?: boolean
}) {
  const { t } = useTranslation()
  const id = storeSellerId(seller?.id ?? sellerId)
  if (!id) return null
  const name = seller?.display_name || seller?.username
  const href = `/store?seller_id=${id}`
  const emailLink = storeSellerMailto(seller?.contact_email)
  if (compact) {
    return (
      <a
        href={href}
        aria-label={t('Shop by {{name}}', {
          name: name || `${t('User ID')}: ${id}`,
        })}
        className='text-muted-foreground hover:text-foreground focus-visible:outline-ring flex min-h-11 min-w-0 items-center gap-2 rounded-sm text-xs focus-visible:outline-2'
      >
        {name && (
          <Avatar className='size-5 shrink-0'>
            <AvatarFallback style={getUserAvatarStyle(name)}>
              {getUserAvatarFallback(name)}
            </AvatarFallback>
          </Avatar>
        )}
        <span className='truncate'>{name || `${t('User ID')}: ${id}`}</span>
      </a>
    )
  }
  return (
    <div className='text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-sm'>
      {name && (
        <a href={href} aria-label={t('Shop by {{name}}', { name })}>
          <Avatar className='size-8'>
            {seller?.avatar_url &&
              /^https:\/\/gravatar\.com\/avatar\/[a-f0-9]{64}\?d=404&r=g&s=192$/.test(
                seller.avatar_url
              ) && (
                <img
                  src={seller.avatar_url}
                  alt=''
                  referrerPolicy='no-referrer'
                  className='absolute inset-0 z-[1] size-full rounded-full object-cover'
                  onError={(event) => {
                    event.currentTarget.hidden = true
                  }}
                />
              )}
            <AvatarFallback style={getUserAvatarStyle(name)}>
              {getUserAvatarFallback(name)}
            </AvatarFallback>
          </Avatar>
        </a>
      )}
      {name && (
        <a
          href={href}
          className='text-foreground font-medium break-all hover:underline'
        >
          {name}
        </a>
      )}
      {seller?.display_name &&
        seller.username &&
        seller.display_name !== seller.username && (
          <span className='break-all'>@{seller.username}</span>
        )}
      <a href={href} className='hover:underline'>
        {t('User ID')}: {id}
      </a>
      {emailLink && (
        <a
          href={emailLink}
          aria-label={t('Seller contact')}
          className='break-all hover:underline'
        >
          {seller?.contact_email}
        </a>
      )}
    </div>
  )
}
