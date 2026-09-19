/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { createFileRoute } from '@tanstack/react-router'

import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { WebMcpPage } from '@/features/webmcp/intro'

export const Route = createFileRoute('/webmcp/')({
  component: () => (
    <ForgePublicShell>
      <WebMcpPage />
    </ForgePublicShell>
  ),
})
