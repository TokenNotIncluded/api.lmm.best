/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createFileRoute } from '@tanstack/react-router'

import { parseSupportSearch } from '@/features/store/support-helpers'
import { StoreSupportPage } from '@/features/store/support-page'

export const Route = createFileRoute('/store/support')({
  validateSearch: parseSupportSearch,
  component: Page,
})

function Page() {
  return <StoreSupportPage search={Route.useSearch()} />
}
