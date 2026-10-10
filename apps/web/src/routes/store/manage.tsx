/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreSellerPage } from '@/features/store/seller-page'

export const Route = createFileRoute('/store/manage')({
  component: StoreSellerPage,
})
