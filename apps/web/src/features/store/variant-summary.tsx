/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'
import { StoreAmount } from './shared'
import type { StoreProduct, StoreVariant } from './types'

export function StoreProductPrice({ product }: { product: StoreProduct }) {
  const { t } = useTranslation()
  const low = product.price_min_quota
  const high = product.price_max_quota
  return Number.isSafeInteger(low) && Number.isSafeInteger(high) && (low ?? 0) > 0 && (high ?? 0) >= (low ?? 0)
    ? <span><StoreAmount quota={low!} />{high !== low && <> – <StoreAmount quota={high!} /></>}</span>
    : product.variants !== undefined ? <span>{t('No variants are currently available.')}</span> : <StoreAmount quota={product.price_quota} />
}
export function StoreInventoryTotals({ product, variant }: { product: StoreProduct; variant?: StoreVariant }) {
  const { t } = useTranslation()
  const total = variant?.inventory_total ?? product.inventory_total
  const available = variant?.inventory_available ?? product.inventory_available
  const sale = variant?.sale_available ?? product.sale_available ?? product.available_stock
  return <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
    {total !== undefined && <span>{t('Inventory total: {{count}}', { count: total })}</span>}
    {available !== undefined && <span>{t('Unreserved inventory: {{count}}', { count: available })}</span>}
    {variant && <span>{t('Reserved inventory: {{count}}', { count: variant.reserved_stock })}</span>}
    <span>{t('Available to buy: {{count}}', { count: sale })}</span>
  </div>
}
