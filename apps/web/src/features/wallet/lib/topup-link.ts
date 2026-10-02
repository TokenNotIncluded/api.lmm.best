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
const TOPUP_AMOUNT_PATTERN = /^[1-9][0-9]{0,6}$/
export const MAX_LINK_TOPUP_AMOUNT = 1_000_000

export function parseWalletTopupAmount(value: unknown): number | undefined {
  if (typeof value === 'string' && !TOPUP_AMOUNT_PATTERN.test(value)) {
    return undefined
  }
  if (typeof value !== 'string' && typeof value !== 'number') {
    return undefined
  }
  const amount = Number(value)
  return Number.isSafeInteger(amount) &&
    amount > 0 &&
    amount <= MAX_LINK_TOPUP_AMOUNT
    ? amount
    : undefined
}

/** URL values only prefill the form. They never select, confirm or start payment. */
export function getWalletTopupPrefill(
  search: string,
  initialAmount?: number
): number | null {
  const amounts = new URLSearchParams(search).getAll('topup_amount')
  if (amounts.length > 0) {
    return amounts.length === 1
      ? (parseWalletTopupAmount(amounts[0]) ?? null)
      : null
  }
  return parseWalletTopupAmount(initialAmount) ?? null
}
