/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreOrdersPage } from '@/features/store/orders-page'

export const Route = createFileRoute('/store/orders')({
  component: StoreOrdersPage,
})
