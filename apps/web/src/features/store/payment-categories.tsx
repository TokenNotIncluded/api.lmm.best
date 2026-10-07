/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

import { storeApi } from './api'
import { STORE_PAYMENT_CATEGORY_COPY as copy } from './payment-category-copy'
import { StoreError } from './shared'
import type { StorePaymentCategories } from './types'

export function StorePaymentCategoriesForm({
  categories,
  onSaved,
}: {
  categories: StorePaymentCategories
  onSaved: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState(categories)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await storeApi.savePaymentCategories(draft)
      await onSaved()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      className='space-y-3 border-y py-4'
      onSubmit={(event) => void save(event)}
    >
      <h2 className='font-semibold'>{t(copy.title)}</h2>
      <p className='text-muted-foreground max-w-prose text-sm'>
        {t(copy.hint)}
      </p>
      <div className='grid gap-3 sm:grid-cols-2'>
        {(['platform_enabled', 'external_enabled'] as const).map((field) => (
          <div
            key={field}
            className='flex items-center justify-between gap-4 rounded-md border p-3 text-sm'
          >
            <Label htmlFor={`store-category-${field}`}>
              {t(
                field === 'platform_enabled'
                  ? 'Platform payments'
                  : 'External payments'
              )}
            </Label>
            <Switch
              id={`store-category-${field}`}
              checked={draft[field]}
              disabled={busy}
              aria-label={t(
                field === 'platform_enabled'
                  ? 'Platform payments'
                  : 'External payments'
              )}
              onCheckedChange={(enabled) =>
                setDraft((current) => ({ ...current, [field]: enabled }))
              }
            />
          </div>
        ))}
      </div>
      <p className='text-muted-foreground text-xs'>{t(copy.retained)}</p>
      <StoreError error={error} />
      <Button type='submit' variant='outline' size='sm' disabled={busy}>
        {t(busy ? 'Saving...' : copy.save)}
      </Button>
    </form>
  )
}
