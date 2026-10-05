/*
Copyright (C) 2026 LIghtJUNction
*/
import { normalizeWalletDisplayCurrencyPreference } from '@/lib/currency'
import type { WalletDisplayCurrencyPreference } from '@/stores/wallet-currency-preference-store'

import { parseSettlementSettings } from './settlement-currency'

export function walletDisplayCurrencyPreference(
  setting: unknown
): WalletDisplayCurrencyPreference {
  return normalizeWalletDisplayCurrencyPreference(
    parseSettlementSettings(setting).wallet_display_currency
  )
}

type WalletDisplayOwner = {
  user: { id: number } | null
  session: { sid: string } | null
  accessToken: string | null
}

export function walletDisplayOwnerKey(owner: WalletDisplayOwner): string {
  return JSON.stringify([owner.user?.id, owner.session?.sid, owner.accessToken])
}

export function sameWalletDisplayOwner(
  current: WalletDisplayOwner,
  original: WalletDisplayOwner
): boolean {
  return walletDisplayOwnerKey(current) === walletDisplayOwnerKey(original)
}
