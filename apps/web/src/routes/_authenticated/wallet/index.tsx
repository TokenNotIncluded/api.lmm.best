/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { Wallet } from '@/features/wallet'
import { parseWalletTopupAmount } from '@/features/wallet/lib/topup-link'

const walletSearchSchema = z.object({
  show_history: z.boolean().optional(),
  topup_amount: z.preprocess(
    parseWalletTopupAmount,
    z.number().int().min(1).max(1_000_000).optional()
  ),
})

export const Route = createFileRoute('/_authenticated/wallet/')({
  component: RouteComponent,
  validateSearch: walletSearchSchema,
})

function RouteComponent() {
  const { show_history, topup_amount } = Route.useSearch()
  return (
    <Wallet initialShowHistory={show_history} initialTopupAmount={topup_amount} />
  )
}
