/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { createFileRoute } from '@tanstack/react-router'

import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { SignalGamePage } from '@/features/signal-game/page'

export const Route = createFileRoute('/games/signal/')({
  component: () => (
    <ForgePublicShell>
      <SignalGamePage />
    </ForgePublicShell>
  ),
})
