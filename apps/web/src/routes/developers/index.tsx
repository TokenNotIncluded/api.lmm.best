/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { createFileRoute } from '@tanstack/react-router'

import { DevelopersPage } from '@/features/developers/page'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'

export const Route = createFileRoute('/developers/')({
  component: () => (
    <ForgePublicShell>
      <DevelopersPage />
    </ForgePublicShell>
  ),
})
