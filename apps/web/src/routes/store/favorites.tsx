/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreFavoritesPage } from '@/features/store/favorites-page'

export const Route = createFileRoute('/store/favorites')({
  component: StoreFavoritesPage,
})
