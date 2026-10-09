/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { MessageCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { storeSupportHref } from './support-helpers'

export function StoreContactButton({ productId, orderId, buyer = true }: { productId?: string; orderId?: string; buyer?: boolean }) {
  const { t } = useTranslation()
  return (
    <Button variant='secondary' className='min-h-11 rounded-full' render={<a href={storeSupportHref({ product: productId, order: orderId, role: buyer ? 'buyer' : 'seller' })} />}>
      <MessageCircle className='size-4' aria-hidden='true' />
      {t(buyer ? 'Message seller' : 'Message buyer')}
    </Button>
  )
}
