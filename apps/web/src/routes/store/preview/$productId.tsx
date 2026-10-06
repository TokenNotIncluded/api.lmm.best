/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreProductPage } from '@/features/store/product-page'

export const Route = createFileRoute('/store/preview/$productId')({
  validateSearch: (search: Record<string, unknown>) => ({
    promotion: typeof search.promotion === 'string' ? search.promotion : '',
    variant_id:
      typeof search.variant_id === 'string' &&
      /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(search.variant_id)
        ? search.variant_id
        : undefined,
    quantity:
      Number.isSafeInteger(Number(search.quantity)) &&
      Number(search.quantity) > 0 &&
      Number(search.quantity) <= 1000
        ? Number(search.quantity)
        : 1,
    cart_item_id:
      typeof search.cart_item_id === 'string' &&
      /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(search.cart_item_id)
        ? search.cart_item_id
        : undefined,
  }),
  component: Page,
})
function Page() {
  const { productId } = Route.useParams()
  const { promotion, variant_id, quantity, cart_item_id } = Route.useSearch()
  return (
    <StoreProductPage
      id={productId}
      ownerPreview
      promotionCode={promotion}
      initialVariantId={variant_id}
      initialQuantity={quantity}
      cartItemId={cart_item_id}
    />
  )
}
