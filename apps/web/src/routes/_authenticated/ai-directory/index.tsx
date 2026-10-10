/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { createFileRoute } from '@tanstack/react-router'

import { AIDirectory } from '@/features/ai-directory'

export const Route = createFileRoute('/_authenticated/ai-directory/')({
  component: AIDirectory,
})
