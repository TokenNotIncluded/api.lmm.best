/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type WalletDisplayCurrency = 'CREDIT' | 'CNY' | 'USD'
export type WalletDisplayCurrencyPreference = '' | WalletDisplayCurrency

/** This store contains anonymous preferences only; account preferences live on the user. */
export const useWalletCurrencyPreferenceStore = create<{
  preference: WalletDisplayCurrencyPreference
  setPreference: (preference: WalletDisplayCurrencyPreference) => void
}>()(
  persist(
    (set) => ({
      preference: '',
      setPreference: (preference) => set({ preference }),
    }),
    {
      name: 'anonymous-wallet-currency-preference',
      merge: (persisted, current) => {
        const value = (persisted as { preference?: unknown } | undefined)
          ?.preference
        return {
          ...current,
          preference:
            value === 'CREDIT' || value === 'CNY' || value === 'USD'
              ? value
              : '',
        }
      },
    }
  )
)
