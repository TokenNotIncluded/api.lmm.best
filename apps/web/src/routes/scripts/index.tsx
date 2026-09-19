/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { createFileRoute } from '@tanstack/react-router'

import { ForgePublicShell } from '@/features/forge/forge-public-shell'
import { PublicScriptsPage } from '@/features/scripts/scripts-panel'

export const Route = createFileRoute('/scripts/')({
  component: () => (
    <ForgePublicShell>
      <PublicScriptsPage />
    </ForgePublicShell>
  ),
})
