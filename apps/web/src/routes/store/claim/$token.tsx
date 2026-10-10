/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { StoreClaimPage } from '@/features/store/claim-page'

export const Route = createFileRoute('/store/claim/$token')({ component: Page })

function Page() {
  const { token } = Route.useParams()
  return <StoreClaimPage token={token} />
}
