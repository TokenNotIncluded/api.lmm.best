/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreProductPage } from '@/features/store/product-page'

export const Route = createFileRoute('/store/preview/$productId')({
  validateSearch: (search: Record<string, unknown>) => ({
    promotion: typeof search.promotion === 'string' ? search.promotion : '',
  }),
  component: Page,
})
function Page() {
  const { productId } = Route.useParams()
  const { promotion } = Route.useSearch()
  return (
    <StoreProductPage id={productId} ownerPreview promotionCode={promotion} />
  )
}
