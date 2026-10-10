/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useMemo, useState, type ReactNode } from 'react'

import {
  PaymentCurrencyContext,
  type PaymentDisplayPreference,
} from './use-payment-currency'

/** Fiat display only. This state never changes settlement currency or order quotas. */
export function PaymentCurrencyProvider({ children }: { children: ReactNode }) {
  const [preference, setPreference] = useState<PaymentDisplayPreference>('')
  const value = useMemo(() => ({ preference, setPreference }), [preference])
  return (
    <PaymentCurrencyContext value={value}>{children}</PaymentCurrencyContext>
  )
}
