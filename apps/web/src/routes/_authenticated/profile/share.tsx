/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { createFileRoute } from '@tanstack/react-router'

import { ProfileSharePage } from '@/features/profile/share-page'

export const Route = createFileRoute('/_authenticated/profile/share')({
  component: ProfileSharePage,
})
