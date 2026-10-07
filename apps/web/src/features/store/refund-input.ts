/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  displayAmountToQuota,
  type WalletDisplayCurrency,
} from '@/lib/currency'
import type { CurrencyConfig } from '@/stores/system-config-store'

import {
  storeRefundNativeAmountFromInput,
  storeRefundNativeAmountSupported,
} from './refund-amount'
import type {
  StoreRefundInput,
  StoreRefundMode,
  StoreRefundView,
} from './refund-types'

export function storeRefundInteger(value: string): number | undefined {
  if (!/^[1-9][0-9]*$/.test(value)) return undefined
  const number = Number(value)
  return Number.isSafeInteger(number) ? number : undefined
}

function positive(value: number | undefined): value is number {
  return value !== undefined && Number.isSafeInteger(value) && value > 0
}

export function storeRefundQuantityMax(view: StoreRefundView): number {
  if (
    !Number.isSafeInteger(view.quantity) ||
    view.quantity <= 0 ||
    !Number.isSafeInteger(view.refunded_quantity) ||
    view.refunded_quantity < 0 ||
    view.refunded_quantity > view.quantity
  ) {
    return 0
  }
  if (!positive(view.max_quantity)) return 0
  // The server accounts for original discounts, prior amount refunds, reserved
  // requests and minor-unit rounding. Its limit is the refund quantity authority.
  return Math.max(
    0,
    Math.min(
      view.max_quantity,
      view.quantity - view.refunded_quantity,
      view.eligible_items.length
    )
  )
}

export function storeRefundAmountMax(view: StoreRefundView): number {
  const value =
    view.payment_method === 'balance'
      ? view.remaining_quota
      : view.native_basis_verified
        ? view.remaining_amount_minor
        : undefined
  return positive(value) ? value : 0
}

export function storeRefundModes(view: StoreRefundView): StoreRefundMode[] {
  if (!positive(view.remaining_quota)) return []
  const modes: StoreRefundMode[] = ['full']
  const partial =
    view.payment_method === 'balance' || view.native_basis_verified
  if (partial && view.supports_quantity && storeRefundQuantityMax(view) > 0) {
    modes.push('quantity')
  }
  if (
    partial &&
    view.supports_amount &&
    (view.payment_method === 'balance' ||
      storeRefundNativeAmountSupported(view.currency)) &&
    storeRefundAmountMax(view) > 0
  ) {
    modes.push('amount')
  }
  return modes
}

export function storeRefundInput(
  view: StoreRefundView,
  draft: {
    mode: StoreRefundMode
    reason: string
    quantity: string
    amount: string
    stockIds: string[]
  },
  requestKey: string,
  balanceDisplay?: {
    currency: WalletDisplayCurrency
    config: CurrencyConfig
  }
): StoreRefundInput | undefined {
  const reason = draft.reason.trim()
  if (
    !requestKey ||
    !reason ||
    reason.length > 1000 ||
    !storeRefundModes(view).includes(draft.mode)
  ) {
    return undefined
  }
  const input: StoreRefundInput = {
    request_key: requestKey,
    mode: draft.mode,
    reason,
  }
  if (draft.mode === 'quantity') {
    const quantity = storeRefundInteger(draft.quantity)
    if (quantity === undefined || quantity > storeRefundQuantityMax(view)) {
      return undefined
    }
    input.quantity = quantity
    if (draft.stockIds.length) {
      const eligible = new Set(view.eligible_items.map((item) => item.stock_id))
      if (
        draft.stockIds.length !== quantity ||
        new Set(draft.stockIds).size !== quantity ||
        draft.stockIds.some((id) => !eligible.has(id))
      ) {
        return undefined
      }
      input.stock_ids = [...draft.stockIds]
    }
  }
  if (draft.mode === 'amount') {
    let amount: number | undefined
    if (view.payment_method !== 'balance') {
      amount = storeRefundNativeAmountFromInput(draft.amount, view.currency)
    } else if (!balanceDisplay) {
      amount = storeRefundInteger(draft.amount)
    } else if (
      draft.amount.length <= 512 &&
      /^\d+(?:\.\d+)?$/.test(draft.amount) &&
      (balanceDisplay.currency !== 'CREDIT' ||
        storeRefundInteger(draft.amount) !== undefined)
    ) {
      amount = displayAmountToQuota(
        draft.amount,
        balanceDisplay.currency,
        balanceDisplay.config
      )
    }
    if (!positive(amount) || amount > storeRefundAmountMax(view)) {
      return undefined
    }
    if (view.payment_method === 'balance') input.amount_quota = amount
    else input.amount_minor = amount
  }
  return input
}

const storageKey = (owner: string, orderId: string) =>
  `store-refund-pending:${owner}:${orderId}`

// Persist only the idempotent request body. Never store pickup tokens/codes,
// card contents, or payment credentials in browser storage.
export function storeRefundPending(
  owner: string,
  orderId: string,
  input?: StoreRefundInput | null
): StoreRefundInput | undefined {
  try {
    const key = storageKey(owner, orderId)
    if (input === null) {
      sessionStorage.removeItem(key)
      return undefined
    }
    if (input) {
      sessionStorage.setItem(key, JSON.stringify(input))
      return input
    }
    const body = sessionStorage.getItem(key)
    if (!body) return undefined
    const value = JSON.parse(body) as StoreRefundInput
    if (
      typeof value.request_key !== 'string' ||
      typeof value.reason !== 'string' ||
      !['full', 'quantity', 'amount'].includes(value.mode)
    ) {
      return undefined
    }
    return value
  } catch {
    return input || undefined
  }
}
