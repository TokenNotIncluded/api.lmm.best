/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { storeSellerId, storeSellerMailto } from './merchant-profile'
import type { StoreSeller } from './types'

export function StoreMerchantIdentity({
  seller,
  sellerId,
}: {
  seller?: StoreSeller | null
  sellerId?: number
}) {
  const { t } = useTranslation()
  const id = storeSellerId(seller?.id ?? sellerId)
  if (!id) return null
  const name = seller?.display_name || seller?.username
  const href = `/store?seller_id=${id}`
  const emailLink = storeSellerMailto(seller?.contact_email)
  return (
    <div className='text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-sm'>
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
