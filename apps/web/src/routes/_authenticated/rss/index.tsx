/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { createFileRoute } from '@tanstack/react-router'

import { RSSReader } from '@/features/rss'

export const Route = createFileRoute('/_authenticated/rss/')({
  component: RSSReader,
})
