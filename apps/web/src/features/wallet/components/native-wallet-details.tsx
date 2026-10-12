/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import {
  nativeCreditDisplay,
  walletProjection,
  type WalletProjectionUser,
} from '../lib/wallet-projection'

/** Separate native ledger buckets, without assigning legacy money units. */
export function NativeWalletDetails({
  user,
}: {
  user: WalletProjectionUser | null | undefined
}) {
  const { t } = useTranslation()
  const projection = walletProjection(user)
  if (!projection.native) return null
  return (
    <div
      className='relative z-10 mt-3 space-y-2 text-xs'
      data-native-wallet-details
    >
      <p>
        {t('Credit conversion unavailable')}: USD / CNY — {t('Unknown')}
      </p>
      <dl className='flex flex-wrap gap-x-4 gap-y-2'>
        {(
          [
            [
              'wallet_spendable_quota',
              t('钱包原生投影：可花积分'),
              projection.spendable,
            ],
            [
              'wallet_reserved_quota',
              t('钱包原生投影：预留积分'),
              projection.reserved,
            ],
            ['wallet_debt', t('钱包原生投影：欠款积分'), projection.debt],
          ] as const
        ).map(([field, label, value]) => (
          <div key={field} data-wallet-bucket={field}>
            <dt className='text-muted-foreground break-all'>{label}</dt>
            <dd className='tabular-nums'>
              {nativeCreditDisplay(value, t('Credits'), t('Unknown'))}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  )
}
