/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'

import { useWalletCurrency } from '@/hooks/use-wallet-currency'

/** Validate a selected-currency input before crossing the raw integer ledger boundary. */
export function marketQuota(
  raw: string,
  convert: (value: string) => number
): number {
  if (
    raw.length > 64 ||
    !/^\d+(\.\d{1,30})?$/.test(raw.trim()) ||
    (convert === Number && /\.[0-9]*[1-9]/.test(raw.trim()))
  ) {
    throw new Error('Invalid amount')
  }
  const quota = convert(raw.trim())
  if (
    !Number.isSafeInteger(quota) ||
    quota < 0 ||
    (quota === 0 && /[1-9]/.test(raw))
  ) {
    throw new Error('Invalid amount')
  }
  return quota
}

/** A draft owns Credits; display/rate changes never reinterpret its stored balance. */
export function useMarketMoneyDraft(initialQuota: number) {
  const { currency, quotaToInput, amountToQuota } = useWalletCurrency()
  const key = `${currency}:${quotaToInput(1)}`
  const [draft, setDraft] = useState<{
    quota: number | undefined
    key?: string
    input?: string
  }>(() => ({
    quota:
      Number.isSafeInteger(initialQuota) && initialQuota >= 0
        ? initialQuota
        : undefined,
  }))
  return {
    quota: draft.quota,
    input:
      draft.key === key
        ? (draft.input ?? '')
        : draft.quota === undefined
          ? ''
          : quotaToInput(draft.quota),
    setInput(input: string) {
      let quota: number | undefined
      try {
        quota = marketQuota(input, amountToQuota)
      } catch {
        quota = undefined
      }
      setDraft({ input, key, quota })
    },
    setQuota(quota: number) {
      setDraft({
        quota: Number.isSafeInteger(quota) && quota >= 0 ? quota : undefined,
      })
    },
  }
}

export function marketNetQuota(priceQuota: number, feeBps: number): number {
  if (
    !Number.isSafeInteger(priceQuota) ||
    priceQuota < 0 ||
    !Number.isSafeInteger(feeBps) ||
    feeBps < 0 ||
    feeBps > 10000
  ) {
    throw new Error('Invalid amount')
  }
  const price = BigInt(priceQuota)
  const fee = (price * BigInt(feeBps)) / 10000n
  return Number(price - fee)
}
