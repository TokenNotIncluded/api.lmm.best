/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreCartPage } from '@/features/store/cart-page'

export const Route = createFileRoute('/store/cart')({
  component: StoreCartPage,
})
