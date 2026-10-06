/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreSettingsPage } from '@/features/store/settings-page'

export const Route = createFileRoute('/store/settings')({
  component: StoreSettingsPage,
})
