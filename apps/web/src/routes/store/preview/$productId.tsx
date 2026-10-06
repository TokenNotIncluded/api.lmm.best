/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreProductPage } from '@/features/store/product-page'
export const Route = createFileRoute('/store/preview/$productId')({
  component: Page,
})
function Page() {
  const { productId } = Route.useParams()
  return <StoreProductPage id={productId} ownerPreview />
}
