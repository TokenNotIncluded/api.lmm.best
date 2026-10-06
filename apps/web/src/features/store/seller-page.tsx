/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { MarketAIReviewHistory } from '@/features/market-ai-review/history'
import { useAuthStore } from '@/stores/auth-store'

import { storeApi } from './api'
import { useStoreMoneyDraft } from './money'
import {
  StoreAmount,
  StoreAuthGate,
  StoreBadges,
  StoreError,
  StoreLoading,
} from './shared'
import type {
  StorePaymentMethod,
  StoreProduct,
  StoreProductInput,
} from './types'
import {
  paymentLabel,
  MAX_IMPORT_BYTES,
  parseInventoryText,
  safeStoreUrl,
  storeRequestKey,
} from './utils'

const EMPTY: StoreProductInput = {
  title: '',
  description: '',
  image_urls: [],
  contact: '',
  links: [],
  price_quota: 500000,
  template: 'card-key',
  delivery_strategy: 'sequential',
  payment_methods: [],
  pickup_login_required: true,
  pickup_code_required: false,
  email_pickup_link: false,
}
export function StoreSellerPage() {
  return (
    <StoreAuthGate>
      <StoreSellerCenter />
    </StoreAuthGate>
  )
}
function StoreSellerCenter() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const user = useAuthStore((state) => state.auth.user)!
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['store', 'my-products', user.id, page],
    queryFn: () => storeApi.myProducts(page),
    retry: false,
  })
  const config = useQuery({
    queryKey: ['store', 'config'],
    queryFn: storeApi.config,
    retry: false,
  })
  const settings = useQuery({
    queryKey: ['store', 'payments', user.id],
    queryFn: storeApi.paymentSettings,
    retry: false,
  })
  const promotionKeys = useRef(new Map<string, string>())
  const [editing, setEditing] = useState<StoreProduct | 'new' | null>(null)
  const [inventory, setInventory] = useState<StoreProduct | null>(null)
  const [aiReviewId, setAIReviewId] = useState<string | null>(null)
  const [promoting, setPromoting] = useState<StoreProduct | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<unknown>(null)
  async function action(product: StoreProduct, fn: () => Promise<unknown>) {
    if (busy !== null) return
    setBusy(product.id)
    setError(null)
    try {
      await fn()
      await client.invalidateQueries({
        queryKey: ['market-ai-reviews', user.id, 'product', product.id],
      })
      await client.invalidateQueries({
        queryKey: ['store', 'my-products', user.id],
      })
      if (promoting) promotionKeys.current.delete(promoting.id)
      setPromoting(null)
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(null)
    }
  }
  const allowedMethods: StorePaymentMethod[] =
    settings.data?.items
      ?.filter((gateway) => gateway.enabled && gateway.configured)
      .map((gateway) => gateway.provider) || []
  return (
    <div className='space-y-5'>
      <div className='flex flex-wrap items-end justify-between gap-3'>
        <div>
          <h1 className='console-page-title text-xl font-bold'>
            {t('Seller center')}
          </h1>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t(
              'Create a product, add inventory, then submit it for administrator review.'
            )}
          </p>
        </div>
        <Button onClick={() => setEditing('new')}>{t('New product')}</Button>
      </div>
      <StoreError
        error={error || query.error}
        retry={() => void query.refetch()}
      />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data && (
          <div className='divide-y rounded-lg border'>
            {!query.data.items.length && (
              <p className='text-muted-foreground p-8 text-center text-sm'>
                {t('No products yet')}
              </p>
            )}
            {query.data.items.map((product) => (
              <article key={product.id} className='space-y-3 p-4'>
                <div className='flex flex-wrap items-start justify-between gap-3'>
                  <div className='min-w-0 space-y-1'>
                    <h2 className='font-semibold break-words'>
                      {product.title}
                    </h2>
                    <div className='text-muted-foreground flex flex-wrap items-center gap-3 text-xs'>
                      <span>{t(product.status)}</span>
                      <span>
                        {t('Stock: {{count}}', {
                          count: product.available_stock,
                        })}
                      </span>
                      <StoreAmount quota={product.price_quota} />
                      <StoreBadges product={product} />
                    </div>
                  </div>
                  <div className='flex flex-wrap gap-2'>
                    <Button
                      size='sm'
                      variant='outline'
                      onClick={() => setEditing(product)}
                    >
                      {t('Edit')}
                    </Button>
                    <Button
                      size='sm'
                      variant='outline'
                      onClick={() => setInventory(product)}
                    >
                      {t('Add inventory')}
                    </Button>
                    {['draft', 'rejected'].includes(product.status) && (
                      <Button
                        size='sm'
                        disabled={busy !== null}
                        onClick={() =>
                          void action(product, () =>
                            storeApi.submitProduct(product.id)
                          )
                        }
                      >
                        {t('Submit for review')}
                      </Button>
                    )}
                    {['published', 'paused'].includes(product.status) && (
                      <Button
                        size='sm'
                        variant='outline'
                        disabled={busy !== null}
                        onClick={() =>
                          void action(product, () =>
                            storeApi.pauseProduct(
                              product.id,
                              product.status !== 'paused'
                            )
                          )
                        }
                      >
                        {t(
                          product.status === 'paused'
                            ? 'Resume trading'
                            : 'Pause trading'
                        )}
                      </Button>
                    )}
                    {product.status === 'published' && (
                      <Button
                        size='sm'
                        variant='outline'
                        onClick={() => setPromoting(product)}
                      >
                        {t('Promote product')}
                      </Button>
                    )}
                  </div>
                </div>
                <MarketAIReviewHistory
                  source='product'
                  id={product.id}
                  lazy
                  open={aiReviewId === product.id}
                  onOpenChange={(open) =>
                    setAIReviewId(open ? product.id : null)
                  }
                />
                {product.review_note && (
                  <p className='bg-muted rounded-md px-3 py-2 text-sm'>
                    {t('Review note')}: {product.review_note}
                  </p>
                )}
              </article>
            ))}
          </div>
        )
      )}
      {query.data && (
        <div className='flex justify-end gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={page === 1}
            onClick={() => setPage((value) => value - 1)}
          >
            {t('Previous page')}
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={!query.data.has_more}
            onClick={() => setPage((value) => value + 1)}
          >
            {t('Next page')}
          </Button>
        </div>
      )}
      <p className='text-muted-foreground text-xs'>
        {t(
          'Edits to approved product details require another review. Inventory contents are never shown publicly.'
        )}
      </p>
      {editing !== null && (
        <StoreProductEditor
          key={editing === 'new' ? 'new' : editing.id}
          product={editing === 'new' ? undefined : editing}
          allowedMethods={allowedMethods}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null)
            await client.invalidateQueries({
              queryKey: ['store', 'my-products', user.id],
            })
          }}
        />
      )}
      {inventory && (
        <StoreInventoryImport
          key={inventory.id}
          product={inventory}
          onClose={() => setInventory(null)}
          onSaved={async () => {
            setInventory(null)
            await client.invalidateQueries({
              queryKey: ['store', 'my-products', user.id],
            })
          }}
        />
      )}
      <Dialog
        open={!!promoting}
        onOpenChange={(open) => {
          if (!open) setPromoting(null)
        }}
      >
        <DialogContent>
          <DialogTitle>{t('Promote product')}</DialogTitle>
          <DialogDescription>
            {t(
              'Promotion lasts one month and moves this product ahead of unpromoted products.'
            )}
          </DialogDescription>
          <div className='space-y-2 text-sm'>
            <strong>{promoting?.title}</strong>
            <p>
              {t('Price')}:{' '}
              {config.data ? (
                <StoreAmount quota={config.data.promotion_quota} />
              ) : (
                '—'
              )}
            </p>
            <p className='text-muted-foreground'>
              {t(
                'The promotion charge is deducted from your platform balance.'
              )}
            </p>
          </div>
          <Button
            disabled={!config.data || busy !== null || !promoting}
            onClick={() =>
              promoting &&
              void action(promoting, () =>
                storeApi.promoteProduct(
                  promoting.id,
                  promotionKeys.current.get(promoting.id) ||
                    (() => {
                      const key = storeRequestKey()
                      promotionKeys.current.set(promoting.id, key)
                      return key
                    })()
                )
              )
            }
          >
            {t('Pay and promote')}
          </Button>
          <StoreError error={error || config.error} />
        </DialogContent>
      </Dialog>
    </div>
  )
}
export function StoreProductEditor({
  product,
  allowedMethods,
  onClose,
  onSaved,
}: {
  product?: StoreProduct
  allowedMethods: StorePaymentMethod[]
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const { t } = useTranslation()
  const price = useStoreMoneyDraft(product?.price_quota || EMPTY.price_quota)
  const [draft, setDraft] = useState<StoreProductInput>(() => ({
    ...EMPTY,
    ...product,
    links: product?.links?.map((link) => ({ ...link })) || [],
    image_urls: [...(product?.image_urls || [])],
  }))
  const [images, setImages] = useState((product?.image_urls || []).join('\n'))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const change = <K extends keyof StoreProductInput>(
    key: K,
    value: StoreProductInput[K]
  ) => setDraft((current) => ({ ...current, [key]: value }))
  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      if (!price.quota || !draft.title.trim()) {
        throw new Error('Enter a valid title and price')
      }
      const imageUrls = images
        .split(/\r?\n/)
        .map((url) => url.trim())
        .filter(Boolean)
      if (
        imageUrls.some((url) => !safeStoreUrl(url)) ||
        draft.links.some((link) => !safeStoreUrl(link.url))
      ) {
        throw new Error('Use valid HTTP or HTTPS links')
      }
      const body: StoreProductInput = {
        title: draft.title.trim(),
        description: draft.description,
        image_urls: imageUrls,
        contact: draft.contact,
        links: draft.links,
        price_quota: price.quota,
        template: draft.template,
        delivery_strategy: draft.delivery_strategy,
        payment_methods: draft.payment_methods,
        pickup_login_required: draft.pickup_login_required,
        pickup_code_required: draft.pickup_code_required,
        email_pickup_link: draft.email_pickup_link,
      }
      if (product) await storeApi.updateProduct(product.id, body)
      else await storeApi.createProduct(body)
      await onSaved()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className='sm:max-w-3xl'>
        <DialogTitle>{t(product ? 'Edit product' : 'New product')}</DialogTitle>
        <DialogDescription>
          {t(
            'Save a draft first. Submit it after adding stock and configuring payment methods.'
          )}
        </DialogDescription>
        <form onSubmit={(event) => void save(event)} className='space-y-5'>
          <StoreError error={error} />
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-2 sm:col-span-2'>
              <Label htmlFor='store-title'>{t('Product title')}</Label>
              <Input
                id='store-title'
                required
                maxLength={200}
                value={draft.title}
                onChange={(event) => change('title', event.target.value)}
              />
            </div>
            <div className='space-y-2 sm:col-span-2'>
              <Label htmlFor='store-description'>{t('Description')}</Label>
              <Textarea
                id='store-description'
                rows={4}
                maxLength={20000}
                value={draft.description}
                onChange={(event) => change('description', event.target.value)}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='store-price'>
                {t('Unit price')} ({price.currency})
              </Label>
              <div className='flex gap-2'>
                <select
                  aria-label={t('Price currency')}
                  className='bg-background rounded-md border px-2 text-sm'
                  value={price.currency}
                  onChange={(event) =>
                    price.setCurrency(
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
                  id='store-price'
                  inputMode='decimal'
                  value={price.input}
                  onChange={(event) => price.setInput(event.target.value)}
                  required
                />
              </div>
              <p className='text-muted-foreground text-xs'>
                {price.quota === undefined
                  ? t('Invalid amount')
                  : `${price.quota.toLocaleString()} ${t('Credits')}`}
              </p>
            </div>
            <div className='space-y-2'>
              <Label htmlFor='store-contact'>{t('Seller contact')}</Label>
              <Input
                id='store-contact'
                maxLength={2000}
                value={draft.contact}
                onChange={(event) => change('contact', event.target.value)}
              />
            </div>
            <div className='space-y-2 sm:col-span-2'>
              <Label htmlFor='store-images'>{t('Product image URLs')}</Label>
              <Textarea
                id='store-images'
                rows={2}
                maxLength={20000}
                value={images}
                onChange={(event) => setImages(event.target.value)}
                placeholder={t('One URL per line')}
              />
            </div>
          </div>
          <fieldset className='space-y-3 border-t pt-4'>
            <legend className='font-semibold'>{t('Product links')}</legend>
            {draft.links.map((link, index) => (
              <div
                key={index}
                className='grid gap-2 sm:grid-cols-[1fr_2fr_auto]'
              >
                <Input
                  aria-label={t('Link title')}
                  value={link.title}
                  maxLength={200}
                  placeholder={t('Link title')}
                  onChange={(event) =>
                    change(
                      'links',
                      draft.links.map((item, position) =>
                        position === index
                          ? { ...item, title: event.target.value }
                          : item
                      )
                    )
                  }
                />
                <Input
                  aria-label={t('Link URL')}
                  value={link.url}
                  placeholder='https://'
                  maxLength={2000}
                  onChange={(event) =>
                    change(
                      'links',
                      draft.links.map((item, position) =>
                        position === index
                          ? { ...item, url: event.target.value }
                          : item
                      )
                    )
                  }
                />
                <Button
                  type='button'
                  variant='ghost'
                  onClick={() =>
                    change(
                      'links',
                      draft.links.filter((_, position) => position !== index)
                    )
                  }
                >
                  {t('Remove')}
                </Button>
                <Input
                  className='sm:col-span-3'
                  aria-label={t('Link description')}
                  value={link.description}
                  placeholder={t('Link description')}
                  maxLength={2000}
                  onChange={(event) =>
                    change(
                      'links',
                      draft.links.map((item, position) =>
                        position === index
                          ? { ...item, description: event.target.value }
                          : item
                      )
                    )
                  }
                />
              </div>
            ))}
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() =>
                change('links', [
                  ...draft.links,
                  { title: '', url: '', description: '' },
                ])
              }
            >
              {t('Add link')}
            </Button>
          </fieldset>
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label htmlFor='store-template'>{t('Delivery template')}</Label>
              <select
                id='store-template'
                className='bg-background h-11 w-full rounded-md border px-3 text-sm'
                value={draft.template}
                onChange={(event) =>
                  change(
                    'template',
                    event.target.value as StoreProductInput['template']
                  )
                }
              >
                <option value='card-key'>{t('Activation keys')}</option>
                <option value='text'>{t('Text items')}</option>
                <option value='custom-text'>{t('Custom text')}</option>
              </select>
            </div>
            <div className='space-y-2'>
              <Label htmlFor='store-delivery'>{t('Delivery strategy')}</Label>
              <select
                id='store-delivery'
                className='bg-background h-11 w-full rounded-md border px-3 text-sm'
                value={draft.delivery_strategy}
                onChange={(event) =>
                  change(
                    'delivery_strategy',
                    event.target.value as 'random' | 'sequential'
                  )
                }
              >
                <option value='sequential'>{t('Sequential delivery')}</option>
                <option value='random'>{t('Random delivery')}</option>
              </select>
            </div>
          </div>
          <fieldset className='space-y-2 border-t pt-4'>
            <legend className='font-semibold'>{t('Payment methods')}</legend>
            {allowedMethods.map((method) => (
              <label
                key={method}
                className='flex items-center justify-between gap-3 py-1 text-sm'
              >
                {t(paymentLabel(method))}
                <Switch
                  checked={draft.payment_methods.includes(method)}
                  onCheckedChange={(enabled) =>
                    change(
                      'payment_methods',
                      enabled
                        ? [...draft.payment_methods, method]
                        : draft.payment_methods.filter(
                            (item) => item !== method
                          )
                    )
                  }
                />
              </label>
            ))}
            {!allowedMethods.length && (
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Configure seller payment methods before submitting this product.'
                )}{' '}
                <a href='/store/settings' className='underline'>
                  {t('Payment settings')}
                </a>
              </p>
            )}
          </fieldset>
          <fieldset className='space-y-3 border-t pt-4'>
            <legend className='font-semibold'>{t('Pickup protection')}</legend>
            {(
              [
                [
                  'pickup_login_required',
                  'Require purchasing account to collect',
                ],
                ['pickup_code_required', 'Require buyer to set a pickup code'],
                ['email_pickup_link', 'Email the pickup link to the buyer'],
              ] as const
            ).map(([key, label]) => (
              <label
                key={key}
                className='flex items-center justify-between gap-4 text-sm'
              >
                <span className='space-y-1'>
                  <span className='block'>{t(label)}</span>
                  {key === 'pickup_login_required' && (
                    <span className='text-muted-foreground block text-xs leading-5'>
                      {t(
                        'If your IP cannot sign in and you have no valid session, sign in from an allowed network first. For collection from any IP, sellers can turn off account-only collection and require a pickup code instead.'
                      )}
                    </span>
                  )}
                </span>
                <Switch
                  checked={draft[key]}
                  onCheckedChange={(value) => change(key, value)}
                />
              </label>
            ))}
          </fieldset>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Prices are stored as whole Credits. 1 USD = 500,000 Credits; CNY uses the current display exchange rate.'
            )}
          </p>
          <div className='flex justify-end gap-2 border-t pt-4'>
            <Button
              type='button'
              variant='outline'
              disabled={busy}
              onClick={onClose}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={busy || price.quota === undefined}>
              {t(busy ? 'Saving...' : 'Save draft')}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
export function StoreInventoryImport({
  product,
  onClose,
  onSaved,
}: {
  product: StoreProduct
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const user = useAuthStore((state) => state.auth.user)!
  const stock = useQuery({
    queryKey: ['store', 'stock', user.id, product.id, page],
    queryFn: () => storeApi.stock(product.id, page),
    retry: false,
  })
  let count = 0
  try {
    count = parseInventoryText(text).length
  } catch {
    /* submit validates size */
  }
  async function loadFile(file?: File) {
    if (!file) return
    try {
      if (file.size > MAX_IMPORT_BYTES) {
        throw new Error('Inventory import is too large')
      }
      const contents = await file.text()
      parseInventoryText(contents)
      setText(contents)
      setError(null)
    } catch (issue) {
      setError(
        issue instanceof Error &&
          issue.message === 'Inventory import is too large'
          ? issue
          : new Error('Could not read this file. Try a UTF-8 text file.')
      )
    }
  }
  async function save() {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const items = parseInventoryText(text)
      if (!items.length) throw new Error('Add at least one inventory item')
      await storeApi.inventory(product.id, items)
      setText('')
      await onSaved()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className='sm:max-w-xl'>
        <DialogTitle>{t('Add inventory')}</DialogTitle>
        <DialogDescription>
          {product.title} ·{' '}
          {t(
            'One text item per line. Empty lines are ignored. Duplicate lines remain separate stock items.'
          )}
        </DialogDescription>
        <StoreError error={error} />
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() =>
              void navigator.clipboard
                .readText()
                .then((value) => {
                  parseInventoryText(value)
                  setText(value)
                  setError(null)
                })
                .catch(() =>
                  setError(
                    new Error(
                      'Clipboard access failed. Paste into the text box instead.'
                    )
                  )
                )
            }
          >
            {t('Paste from clipboard')}
          </Button>
          <label className='hover:bg-muted inline-flex cursor-pointer items-center rounded-md border px-3 py-2 text-xs font-medium'>
            {t('Import file')}
            <input
              type='file'
              accept='.txt,.csv,text/plain,text/csv'
              className='sr-only'
              onChange={(event) => {
                void loadFile(event.target.files?.[0])
                event.target.value = ''
              }}
            />
          </label>
        </div>
        <Textarea
          rows={9}
          value={text}
          onChange={(event) => setText(event.target.value)}
          aria-label={t('Inventory text')}
          placeholder={t('One item per line')}
        />
        <p className='text-muted-foreground text-xs'>
          {t('{{count}} items ready to import', { count })} ·{' '}
          {t('Maximum 10,000 items or 2 MB per import.')}
        </p>
        <Button disabled={!count || busy} onClick={() => void save()}>
          {t(busy ? 'Importing...' : 'Import inventory')}
        </Button>
        <section className='space-y-3 border-t pt-4'>
          <h2 className='text-sm font-semibold'>{t('Inventory records')}</h2>
          <StoreError error={stock.error} retry={() => void stock.refetch()} />
          {stock.isPending ? (
            <StoreLoading />
          ) : (
            stock.data && (
              <>
                <div className='divide-y'>
                  {!stock.data.items.length && (
                    <p className='text-muted-foreground py-2 text-xs'>
                      {t('No inventory yet')}
                    </p>
                  )}
                  {stock.data.items.map((item) => (
                    <div
                      key={item.id}
                      className='flex items-center justify-between gap-2 py-2 text-xs'
                    >
                      <span className='min-w-0 break-all'>
                        {item.id} · {t(item.state)}
                      </span>
                      {item.state === 'available' && (
                        <Button
                          size='sm'
                          variant='ghost'
                          disabled={busy}
                          onClick={() => {
                            setBusy(true)
                            setError(null)
                            void storeApi
                              .removeStock(product.id, item.id)
                              .then(() => stock.refetch())
                              .catch((issue) => setError(issue))
                              .finally(() => setBusy(false))
                          }}
                        >
                          {t('Remove')}
                        </Button>
                      )}
                    </div>
                  ))}
                </div>
                <div className='flex justify-end gap-2'>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={page === 1}
                    onClick={() => setPage((value) => value - 1)}
                  >
                    {t('Previous page')}
                  </Button>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={!stock.data.has_more}
                    onClick={() => setPage((value) => value + 1)}
                  >
                    {t('Next page')}
                  </Button>
                </div>
              </>
            )
          )}
        </section>
      </DialogContent>
    </Dialog>
  )
}
