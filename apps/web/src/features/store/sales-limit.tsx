/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

import { storeApi } from './api'
import { STORE_SALES_LIMIT_COPY as copy } from './sales-limit-copy'
import { StoreError } from './shared'
import type { StoreProduct } from './types'

export function StoreSalesLimit({
  product,
  onSaved,
}: {
  product: StoreProduct
  onSaved: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [unlimited, setUnlimited] = useState(product.sale_limit === null)
  const [limit, setLimit] = useState(String(product.sale_limit ?? 0))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const amount = Number(limit)
      if (
        !unlimited &&
        (!/^\d+$/.test(limit) || !Number.isSafeInteger(amount) || amount < 0)
      ) {
        throw new Error(t(copy.invalid))
      }
      await storeApi.saleLimit(product.id, unlimited ? null : amount)
      await onSaved()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      onSubmit={(event) => void save(event)}
      className='space-y-3 rounded-md border p-3'
    >
      <h3 className='text-sm font-semibold'>{t(copy.title)}</h3>
      <StoreError error={error} />
      <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
        <span>{t(copy.inventory, { count: product.available_stock })}</span>
        <span>{t(copy.paid, { count: product.paid_quantity })}</span>
        <span>{t(copy.reserved, { count: product.reserved_quantity })}</span>
        <span>{t(copy.available, { count: product.sale_available })}</span>
      </div>
      <div className='flex items-center justify-between gap-3'>
        <Label htmlFor={`store-unlimited-${product.id}`}>
          {t(copy.unlimited)}
        </Label>
        <Switch
          id={`store-unlimited-${product.id}`}
          checked={unlimited}
          disabled={busy}
          onCheckedChange={setUnlimited}
        />
      </div>
      {!unlimited && (
        <div className='space-y-2'>
          <Label htmlFor={`store-sale-limit-${product.id}`}>
            {t(copy.limit)}
          </Label>
          <Input
            id={`store-sale-limit-${product.id}`}
            inputMode='numeric'
            value={limit}
            onChange={(event) => setLimit(event.target.value)}
            disabled={busy}
          />
        </div>
      )}
      <p className='text-muted-foreground text-xs'>{t(copy.help)}</p>
      <Button type='submit' size='sm' disabled={busy}>
        {t(busy ? 'Saving...' : copy.save)}
      </Button>
    </form>
  )
}
