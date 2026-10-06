/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import {
  formatMinimumQuotaInCurrency,
  getCurrencyFormattingLocale,
} from '@/lib/currency'

import { storeApi } from './api'
import { DELIVERY_TEMPLATES } from './delivery-template'
import { STORE_MINIMUM_PRICE_COPY as minimumCopy } from './minimum-price-copy'
import { useStoreMoneyDraft } from './money'
import { StoreAmount, StoreError } from './shared'
import type { StoreProduct, StoreVariant, StoreVariantInput } from './types'
import { StoreInventoryTotals } from './variant-summary'

export function StoreVariantsManager({
  product,
  minimumPriceQuota,
  onChanged,
}: {
  product: StoreProduct
  minimumPriceQuota?: number
  onChanged: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState<StoreVariant | 'new' | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  if (!product.variants) return null
  return (
    <details className='space-y-3 border-t pt-3'>
      <summary className='focus-visible:outline-ring cursor-pointer text-sm font-semibold'>
        {t('Manage variants')}
      </summary>
      <p className='text-muted-foreground text-xs leading-5'>
        {t(
          'Variant prices, templates and stock are separate. The sale limit and delivery protections are shared by the whole product.'
        )}
      </p>
      <p className='text-muted-foreground text-xs leading-5'>
        {t(
          'Total inventory includes available and reserved items, including disabled variants. Buyable stock is limited by the shared sale limit.'
        )}
      </p>
      <StoreInventoryTotals product={product} />
      <StoreError error={error} />
      <div className='divide-y'>
        {product.variants.map((variant) => (
          <div
            key={variant.id}
            className='flex flex-wrap items-start justify-between gap-3 py-3'
          >
            <div className='min-w-0 flex-1 space-y-2'>
              <p className='text-sm font-medium break-words'>
                {variant.name || t('Default variant')}
                {variant.is_default && (
                  <span className='bg-muted ml-2 rounded px-1.5 py-0.5 text-xs'>
                    {t('Default variant')}
                  </span>
                )}
              </p>
              <p className='text-sm'>
                <StoreAmount quota={variant.price_quota} /> ·{' '}
                {t(DELIVERY_TEMPLATES[variant.template].label)}
              </p>
              <StoreInventoryTotals product={product} variant={variant} />
            </div>
            <div className='flex items-center gap-3'>
              <label className='flex items-center gap-2 text-xs'>
                {t('Variant enabled')}
                <Switch
                  checked={variant.enabled}
                  disabled={busy || editing !== null}
                  onCheckedChange={(enabled) => {
                    setBusy(true)
                    setError(null)
                    void storeApi
                      .enableVariant(product.id, variant.id, enabled)
                      .then(onChanged)
                      .catch((issue) => setError(issue))
                      .finally(() => setBusy(false))
                  }}
                />
              </label>
              <Button
                type='button'
                size='sm'
                variant='outline'
                disabled={busy || editing !== null}
                onClick={() => setEditing(variant)}
              >
                {t('Edit')}
              </Button>
            </div>
          </div>
        ))}
      </div>
      {editing ? (
        <StoreVariantEditor
          key={editing === 'new' ? 'new' : editing.id}
          product={product}
          variant={editing === 'new' ? undefined : editing}
          minimumPriceQuota={minimumPriceQuota}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            await onChanged()
            setEditing(null)
          }}
        />
      ) : (
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={busy || product.variants.length >= 200}
          onClick={() => setEditing('new')}
        >
          {t('Add variant')}
        </Button>
      )}
    </details>
  )
}

function StoreVariantEditor({
  product,
  variant,
  minimumPriceQuota,
  onClose,
  onSaved,
}: {
  product: StoreProduct
  variant?: StoreVariant
  minimumPriceQuota?: number
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const { t, i18n } = useTranslation()
  const wallet = useWalletCurrency()
  const money = useStoreMoneyDraft(variant?.price_quota ?? product.price_quota)
  const [name, setName] = useState(variant?.name ?? '')
  const [template, setTemplate] = useState(
    variant?.template ?? product.template
  )
  const [enabled, setEnabled] = useState(variant?.enabled ?? true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const minimum =
    Number.isSafeInteger(minimumPriceQuota) && (minimumPriceQuota ?? -1) >= 0
      ? minimumPriceQuota
      : undefined
  const validName =
    name.trim().length > 0 &&
    new TextEncoder().encode(name.trim()).length <= 200
  const validPrice =
    money.quota !== undefined &&
    money.quota > 0 &&
    minimum !== undefined &&
    money.quota >= minimum
  const minimumAmount =
    minimum === undefined
      ? ''
      : formatMinimumQuotaInCurrency(
          minimum,
          money.currency,
          {
            creditLabel: t('Credits'),
            locale: getCurrencyFormattingLocale(i18n.language),
          },
          wallet.config
        )
  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (busy || !validName || !validPrice || money.quota === undefined) return
    setBusy(true)
    setError(null)
    try {
      const body: StoreVariantInput = {
        name: name.trim(),
        price_quota: money.quota,
        template,
        enabled,
      }
      if (variant) await storeApi.updateVariant(product.id, variant.id, body)
      else await storeApi.createVariant(product.id, body)
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
      className='space-y-4 border-t pt-4'
    >
      <h3 className='text-sm font-semibold'>
        {t(variant ? 'Edit variant' : 'Add variant')}
      </h3>
      <StoreError error={error} />
      <p className='text-muted-foreground text-xs leading-5'>
        {t(
          'Changing a variant name, price or template returns the product to draft for review. Existing orders keep their original terms.'
        )}
      </p>
      <div className='grid gap-4 sm:grid-cols-2'>
        <div className='space-y-2 sm:col-span-2'>
          <Label htmlFor={`variant-name-${product.id}`}>
            {t('Variant name')}
          </Label>
          <Input
            id={`variant-name-${product.id}`}
            value={name}
            maxLength={200}
            required
            disabled={busy}
            onChange={(event) => setName(event.target.value)}
            placeholder={t('For example: Plus · 2 months')}
          />
          {new TextEncoder().encode(name.trim()).length > 200 && (
            <p role='alert' className='text-destructive text-xs'>
              {t('Variant name is too long. Please shorten it.')}
            </p>
          )}
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`variant-price-${product.id}`}>
            {t('Unit price')}
          </Label>
          <div className='flex gap-2'>
            <select
              aria-label={t('Price currency')}
              className='bg-background h-11 rounded-md border px-2 text-sm'
              value={money.currency}
              disabled={busy}
              onChange={(event) =>
                money.setCurrency(
                  event.target.value as 'USD' | 'CNY' | 'CREDIT'
                )
              }
            >
              {(['USD', 'CNY', 'CREDIT'] as const).map((currency) => (
                <option key={currency} value={currency}>
                  {currency}
                </option>
              ))}
            </select>
            <Input
              id={`variant-price-${product.id}`}
              inputMode='decimal'
              value={money.input}
              required
              disabled={busy}
              onChange={(event) => money.setInput(event.target.value)}
            />
          </div>
          <p className='text-muted-foreground text-xs'>
            {money.quota === undefined
              ? t('Invalid amount')
              : `${money.quota.toLocaleString()} ${t('Credits')}`}
          </p>
          {minimum !== undefined ? (
            <p className='text-muted-foreground text-xs'>
              {t(minimumCopy.current, { amount: minimumAmount })}
            </p>
          ) : (
            <p className='text-warning text-xs'>{t('Loading...')}</p>
          )}
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`variant-template-${product.id}`}>
            {t('Delivery template')}
          </Label>
          <select
            id={`variant-template-${product.id}`}
            className='bg-background h-11 w-full rounded-md border px-3 text-sm'
            value={template}
            disabled={busy}
            onChange={(event) =>
              setTemplate(event.target.value as StoreVariant['template'])
            }
          >
            {Object.entries(DELIVERY_TEMPLATES).map(([key, definition]) => (
              <option key={key} value={key}>
                {t(definition.label)}
              </option>
            ))}
          </select>
        </div>
      </div>
      {!variant && (
        <label className='flex items-center justify-between gap-3 text-sm'>
          {t('Variant enabled')}
          <Switch
            checked={enabled}
            disabled={busy}
            onCheckedChange={setEnabled}
          />
        </label>
      )}
      <div className='flex justify-end gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={busy}
          onClick={onClose}
        >
          {t('Cancel')}
        </Button>
        <Button
          type='submit'
          size='sm'
          disabled={busy || !validName || !validPrice}
        >
          {t(busy ? 'Saving...' : 'Save variant')}
        </Button>
      </div>
    </form>
  )
}
