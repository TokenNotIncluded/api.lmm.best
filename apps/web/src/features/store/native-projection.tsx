/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { nativePriceText, STORE_NATIVE_COPY } from './native-projection-values'
import type { StorePrice, StoreProduct } from './types'

export function StoreNativeAmount({ price }: { price?: StorePrice | null }) {
  const { t } = useTranslation()
  const value = nativePriceText(price)
  return (
    <span className='tabular-nums'>
      {value === undefined
        ? t(STORE_NATIVE_COPY.unknown, { defaultValue: 'Amount unknown' })
        : price?.unit === 'credits'
          ? value
          : t(STORE_NATIVE_COPY.minor, {
              defaultValue: '{{value}} (minor units)',
              value,
            })}
    </span>
  )
}

export function StoreNativeNotice() {
  const { t } = useTranslation()
  return (
    <p className='text-muted-foreground text-sm'>
      {t(STORE_NATIVE_COPY.readonly, {
        defaultValue:
          'Native record · read-only. Legacy checkout, payment, pickup and refund actions are unavailable here. No currency conversion is applied.',
      })}
    </p>
  )
}

export function StoreNativeProduct({ product }: { product: StoreProduct }) {
  const { t } = useTranslation()
  return (
    <aside className='bg-card space-y-4 rounded-lg border p-5'>
      <StoreNativeNotice />
      <ul className='space-y-2'>
        {product.variants?.map((variant) => (
          <li
            key={variant.id}
            className='flex flex-wrap justify-between gap-2 text-sm'
          >
            <span>{variant.name || t('Default variant')}</span>
            <StoreNativeAmount price={variant.price} />
          </li>
        ))}
      </ul>
      <p className='text-muted-foreground text-xs'>
        {t(STORE_NATIVE_COPY.time, { defaultValue: 'Time unknown' })}
      </p>
      <Button disabled>{t('Buy now')}</Button>
    </aside>
  )
}
