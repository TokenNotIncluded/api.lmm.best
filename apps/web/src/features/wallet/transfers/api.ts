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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { api } from '@/lib/api'
import { formatQuotaWithCurrency } from '@/lib/currency'

export interface WalletTransfer {
  id: number
  token: string
  quota: number
  status: 'pending' | 'claimed' | 'cancelled'
  created_at: number
  claimed_at: number
  cancelled_at: number
  recipient_id: number
  recipient_username: string
  recipient_name: string
  recipient_email: string
}
export interface TransferReceipt {
  quota: number
  status: WalletTransfer['status']
  created_at: number
  claimed_at: number
  is_sender: boolean
  claimed_by_me: boolean
}

async function result<T>(
  request: Promise<{ data: { success: boolean; message: string; data: T } }>
): Promise<T> {
  const { data } = await request
  if (!data.success) throw new Error(data.message)
  return data.data
}
export const listTransfers = (before = 0) =>
  result<WalletTransfer[]>(
    api.get('/api/wallet-transfer', {
      params: { before },
      skipBusinessError: true,
    })
  )
export const createTransfer = (quota: number, requestKey: string) =>
  result<WalletTransfer>(
    api.post(
      '/api/wallet-transfer',
      { quota, request_key: requestKey },
      { skipBusinessError: true }
    )
  )
export const cancelTransfer = (id: number) =>
  result<null>(
    api.post(
      `/api/wallet-transfer/${id}/cancel`,
      {},
      { skipBusinessError: true }
    )
  )
export const inspectTransfer = (token: string) =>
  result<TransferReceipt>(
    api.post(
      '/api/wallet-transfer/inspect',
      { token },
      { skipBusinessError: true }
    )
  )
export const claimTransfer = (token: string) =>
  result<TransferReceipt>(
    api.post(
      '/api/wallet-transfer/claim',
      { token },
      { skipBusinessError: true }
    )
  )

// A fragment is never sent in an HTTP request or Referer header. Send the
// credential to the API in a body, never in a URL or analytics event.
export const transferLink = (token: string) =>
  `${window.location.origin}/transfer#${token}`
/** Legacy batch conversion; new wallet inputs use the captured display-currency hook. */
export function transferQuota(
  amount: string,
  quotaPerUnit: number
): number | null {
  if (!/^\d+(\.\d+)?$/.test(amount)) return null
  const value = Number(amount) * quotaPerUnit
  const quota = Math.round(value)
  return Number.isSafeInteger(quota) &&
    quota > 0 &&
    Math.abs(value - quota) < 0.000001
    ? quota
    : null
}

export const formatTransferQuota = (quota: number) =>
  formatQuotaWithCurrency(quota, {
    digitsLarge: 12,
    digitsSmall: 12,
    abbreviate: false,
    compact: false,
  })
