/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { ToolMarket } from '@/features/tool-market'
import { parseMarketServiceID } from '@/features/tool-market/service-link'

export const Route = createFileRoute('/_authenticated/tool-market/')({
  validateSearch: z.object({
    service_id: z.preprocess(parseMarketServiceID, z.string().optional()),
  }),
  component: Page,
})

function Page() {
  const { service_id } = Route.useSearch()
  return <ToolMarket initialServiceID={service_id} />
}
