/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { createFileRoute } from '@tanstack/react-router'

import { ToolMarket } from '@/features/tool-market'

export const Route = createFileRoute('/_authenticated/tool-market/')({
  component: ToolMarket,
})
