/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useEffect } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import type { StoreTrafficPage } from './traffic-page'

export function StoreProductDetailTraffic({
  page,
  product,
}: {
  page: StoreTrafficPage
  product: { id: string; seller_id: number }
}) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const productId = product.id
  const sellerId = product.seller_id
  useEffect(() => {
    if (userId !== sellerId) {
      void page.record({ id: productId, seller_id: sellerId }, 'click')
    }
  }, [page, productId, sellerId, userId])
  return null
}
