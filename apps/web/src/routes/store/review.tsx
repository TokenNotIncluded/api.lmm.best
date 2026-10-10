/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreReviewPage } from '@/features/store/review-page'

export const Route = createFileRoute('/store/review')({
  component: StoreReviewPage,
})
