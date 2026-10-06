/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { storeSellerId } from '@/features/store/merchant-profile'
import { StorePage } from '@/features/store/store-page'

export const Route = createFileRoute('/store/')({
  validateSearch: (search: Record<string, unknown>) => ({
    seller_id: storeSellerId(search.seller_id),
  }),
  component: StoreRoute,
})

function StoreRoute() {
  const { seller_id } = Route.useSearch()
  return <StorePage sellerId={seller_id} />
}
