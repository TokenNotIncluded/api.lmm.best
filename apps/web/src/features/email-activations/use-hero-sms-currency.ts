/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useCallback } from 'react'

import { useWalletCurrency } from '@/hooks/use-wallet-currency'

import { formatHeroSmsPlatformAmount } from './api'
import { SMS_MINIMUM_BALANCE_LEGACY_UNITS } from './sms-balance'

/** Subscribe every quote and charge view to the same captured wallet denomination. */
export function useHeroSmsCurrency() {
  const wallet = useWalletCurrency()
  const formatPrice = useCallback(
    (value: number) => formatHeroSmsPlatformAmount(value, wallet),
    [wallet]
  )
  const formatQuota = useCallback(
    (value: number) =>
      wallet.formatQuota(value, { digitsLarge: 8, digitsSmall: 8 }),
    [wallet]
  )
  return {
    ...wallet,
    formatQuota,
    formatPrice,
    // Keep the established raw eligibility boundary.
    minimumBalance: formatQuota(
      SMS_MINIMUM_BALANCE_LEGACY_UNITS * wallet.config.quotaPerUnit
    ),
  }
}
