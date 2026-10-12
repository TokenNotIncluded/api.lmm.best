/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { AuthUser } from '@/stores/auth-store'

export type WalletProjectionUser = Pick<
  AuthUser,
  | 'quota'
  | 'financial_projection_available'
  | 'usage_projection_available'
  | 'request_count'
  | 'wallet_unit'
  | 'wallet_spendable_quota'
  | 'wallet_reserved_quota'
  | 'wallet_debt'
  | 'wallet_money_basis'
  | 'wallet_money_available'
>

export function isNativeWallet(user: WalletProjectionUser | null | undefined) {
  return (
    user?.wallet_unit !== undefined ||
    user?.wallet_money_available !== undefined
  )
}

export function exactCredit(value: unknown): number | null {
  return typeof value === 'number' && Number.isSafeInteger(value) ? value : null
}

export function walletProjection(
  user: WalletProjectionUser | null | undefined
) {
  const native = isNativeWallet(user)
  const financial = user?.financial_projection_available !== false
  const available = financial
    ? native
      ? user?.wallet_unit === 'core_credit'
        ? exactCredit(user.quota)
        : null
      : (user?.quota ?? 0)
    : null
  const bucket = (value: unknown) => {
    const exact = exactCredit(value)
    return financial && exact !== null && exact >= 0 ? exact : null
  }
  const history = financial && user?.usage_projection_available !== false
  const requests =
    financial && (!native || history)
      ? user?.usage_projection_available !== undefined || native
        ? bucket(user?.request_count)
        : (user?.request_count ?? 0)
      : null
  return {
    native,
    available,
    spendable: bucket(user?.wallet_spendable_quota),
    reserved: bucket(user?.wallet_reserved_quota),
    debt: bucket(user?.wallet_debt),
    history,
    requests,
    // A read-only wallet DTO is not a checkout capability or a money quote.
    readOnly: native,
  }
}

export function nativeCreditDisplay(
  value: number | null,
  creditLabel: string,
  unknownLabel: string
) {
  return value === null
    ? unknownLabel
    : `${value.toLocaleString()} ${creditLabel}`
}
