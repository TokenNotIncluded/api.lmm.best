/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'

import { marketQuota } from '@/features/tool-market/money'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import {
  displayAmountToQuota,
  quotaToDisplayInput,
  type WalletDisplayCurrency,
} from '@/lib/currency'

/** Local price denomination owns integral Credits and never changes wallet preferences. */
export function useStoreMoneyDraft(initialQuota: number) {
  const wallet = useWalletCurrency()
  const [currency, setCurrency] = useState<WalletDisplayCurrency>(
    wallet.currency
  )
  const toInput = (quota: number) =>
    currency === 'CREDIT'
      ? String(quota)
      : quotaToDisplayInput(quota, currency, wallet.config)
  const key = `${currency}:${currency === 'CREDIT' ? '1' : toInput(1)}`
  const [draft, setDraft] = useState<{
    quota?: number
    key?: string
    input?: string
  }>(() => ({
    quota:
      Number.isSafeInteger(initialQuota) && initialQuota >= 0
        ? initialQuota
        : undefined,
  }))
  return {
    currency,
    setCurrency,
    quota: draft.quota,
    input:
      draft.key === key
        ? (draft.input ?? '')
        : draft.quota === undefined
          ? ''
          : toInput(draft.quota),
    setInput(input: string) {
      let quota: number | undefined
      try {
        quota = marketQuota(
          input,
          currency === 'CREDIT'
            ? Number
            : (value) => displayAmountToQuota(value, currency, wallet.config)
        )
      } catch {
        quota = undefined
      }
      setDraft({ input, key, quota })
    },
  }
}
