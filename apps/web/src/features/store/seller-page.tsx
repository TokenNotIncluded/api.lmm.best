/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
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
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { MarketAIReviewHistory } from '@/features/market-ai-review/history'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import {
  formatMinimumQuotaInCurrency,
  getCurrencyFormattingLocale,
} from '@/lib/currency'
import { useAuthStore } from '@/stores/auth-store'

import { storeApi } from './api'
import {
  DELIVERY_TEMPLATES,
  structuredTemplate,
  validateComposedItems,
} from './delivery-template'
import { StoreInventoryComposer } from './inventory-composer'
import { StoreLinkPresetChooser } from './link-presets'
import { STORE_MINIMUM_PRICE_COPY as minimumCopy } from './minimum-price-copy'
import { useStoreMoneyDraft } from './money'
import { STORE_PAYMENT_CATEGORY_COPY as copy } from './payment-category-copy'
import { StoreSalesLimit } from './sales-limit'
import { STORE_SALES_LIMIT_COPY as salesCopy } from './sales-limit-copy'
import {
  StoreAmount,
  StoreAuthGate,
  StoreBadges,
  StoreError,
  StoreLoading,
} from './shared'
import { STORE_TEST_MODE_COPY as testCopy } from './test-mode-copy'
import type {
  StoreLinkPreset,
  StorePaymentMethod,
  StoreProduct,
  StoreProductInput,
  StoreVariant,
  StorePage,
} from './types'
import {
  paymentLabel,
  MAX_IMPORT_BYTES,
  parseInventoryText,
  safeStoreUrl,
  storeRequestKey,
} from './utils'
import { StoreInventoryTotals, StoreProductPrice } from './variant-summary'
import { legacyVariantProduct } from './variant-utils'
import { StoreVariantsManager } from './variants-manager'

const EMPTY: StoreProductInput = {
  title: '',
  description: '',
  image_urls: [],
  contact: '',
  links: [],
  price_quota: 0,
  template: 'card-key',
  delivery_strategy: 'sequential',
  payment_methods: [],
  pickup_login_required: true,
  pickup_code_required: false,
  email_pickup_link: false,
}
export function StoreSellerPage() {
  const user = useAuthStore((state) => state.auth.user)
  return (
    <StoreAuthGate>
      <StoreSellerCenter key={user?.id || 'guest'} />
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
  const [lifecycle, setLifecycle] = useState<{
    product: StoreProduct
    kind: 'unlist' | 'delete'
  } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<unknown>(null)
  async function action(
    product: StoreProduct,
    fn: () => Promise<unknown>,
    onSuccess?: () => void
  ) {
    if (busy !== null) return
    setBusy(product.id)
    setError(null)
    try {
      await fn()
      onSuccess?.()
      await client.invalidateQueries({
        queryKey: ['market-ai-reviews', user.id, 'product', product.id],
      })
      await client.invalidateQueries({
        queryKey: ['store', 'my-products', user.id],
      })
      await client.invalidateQueries({ queryKey: ['store', 'products'] })
      await client.invalidateQueries({
        queryKey: ['store', 'product', product.id],
      })
      await client.invalidateQueries({ queryKey: ['store', 'reviews'] })
      if (promoting) promotionKeys.current.delete(promoting.id)
      setPromoting(null)
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(null)
    }
  }
  function confirmLifecycle() {
    if (!lifecycle) return
    const { product, kind } = lifecycle
    void action(
      product,
      () =>
        kind === 'delete'
          ? storeApi.deleteProduct(product.id)
          : storeApi.unlistProduct(product.id),
      () => {
        client.setQueriesData<StorePage<StoreProduct>>(
          { queryKey: ['store', 'products'] },
          (previous) =>
            previous && {
              ...previous,
              items: previous.items.filter((item) => item.id !== product.id),
            }
        )
        client.removeQueries({
          queryKey: ['store', 'product', product.id],
          exact: true,
        })
        client.setQueriesData<StorePage<StoreProduct>>(
          { queryKey: ['store', 'my-products', user.id] },
          (previous) =>
            previous && {
              ...previous,
              items:
                kind === 'delete'
                  ? previous.items.filter((item) => item.id !== product.id)
                  : previous.items.map((item) =>
                      item.id === product.id
                        ? { ...item, status: 'unlisted' as const }
                        : item
                    ),
            }
        )
        setLifecycle(null)
      }
    )
  }
  const allowedMethods: StorePaymentMethod[] =
    settings.data?.items
      ?.filter((gateway) => gateway.effective_enabled === true)
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
                      <span>
                        {t(
                          product.status === 'off_shelf'
                            ? salesCopy.offShelfStatus
                            : product.status
                        )}
                      </span>
                      <StoreProductPrice product={product} />
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
                    {!product.test_mode &&
                      ['draft', 'rejected'].includes(product.status) && (
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
                    {!product.test_mode && product.status === 'published' && (
                      <Button
                        size='sm'
                        variant='outline'
                        onClick={() => setPromoting(product)}
                      >
                        {t('Promote product')}
                      </Button>
                    )}
                    {product.test_mode && (
                      <Button
                        size='sm'
                        variant='outline'
                        render={<a href={`/store/preview/${product.id}`} />}
                      >
                        {t(testCopy.preview)}
                      </Button>
                    )}
                    {['published', 'paused', 'off_shelf'].includes(
                      product.status
                    ) && (
                      <Button
                        size='sm'
                        variant='outline'
                        disabled={
                          busy !== null ||
                          (product.test_mode === true &&
                            product.status === 'off_shelf')
                        }
                        onClick={() =>
                          void action(product, () =>
                            storeApi.listing(
                              product.id,
                              product.status === 'off_shelf'
                            )
                          )
                        }
                      >
                        {t(
                          product.status === 'off_shelf'
                            ? salesCopy.relist
                            : salesCopy.offShelf
                        )}
                      </Button>
                    )}
                    {product.status !== 'unlisted' && (
                      <Button
                        size='sm'
                        variant='outline'
                        disabled={busy !== null}
                        onClick={() => {
                          setError(null)
                          setLifecycle({ product, kind: 'unlist' })
                        }}
                      >
                        {t('Unlist product')}
                      </Button>
                    )}
                    <Button
                      size='sm'
                      variant='destructive'
                      disabled={busy !== null}
                      onClick={() => {
                        setError(null)
                        setLifecycle({ product, kind: 'delete' })
                      }}
                    >
                      {t('Delete product')}
                    </Button>
                  </div>
                </div>
                <StoreInventoryTotals product={product} />
                <StoreVariantsManager
                  product={product}
                  minimumPriceQuota={config.data?.minimum_unit_price_quota}
                  onChanged={async () => {
                    await Promise.all([
                      client.invalidateQueries({
                        queryKey: ['store', 'my-products', user.id],
                      }),
                      client.invalidateQueries({
                        queryKey: ['store', 'products'],
                      }),
                      client.invalidateQueries({
                        queryKey: ['store', 'product', product.id],
                      }),
                      client.invalidateQueries({
                        queryKey: [
                          'store',
                          'product-preview',
                          product.id,
                          user.id,
                        ],
                      }),
                      client.invalidateQueries({
                        queryKey: [
                          'market-ai-reviews',
                          user.id,
                          'product',
                          product.id,
                        ],
                      }),
                    ])
                  }}
                />
                {product.sale_limit !== undefined && (
                  <StoreSalesLimit
                    key={`${product.id}:${product.updated_at}:${product.sale_limit}`}
                    product={product}
                    onSaved={async () => {
                      await client.invalidateQueries({
                        queryKey: ['store', 'my-products', user.id],
                      })
                    }}
                  />
                )}
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
      <AlertDialog
        open={lifecycle !== null}
        onOpenChange={(open) => {
          if (!open && busy === null) setLifecycle(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(
                lifecycle?.kind === 'delete'
                  ? 'Delete product?'
                  : 'Unlist product?'
              )}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                lifecycle?.kind === 'delete'
                  ? 'Remove {{title}} from your products and the store. Existing orders and delivered items remain accessible.'
                  : 'Remove {{title}} from the store. You can edit and submit it for review again. Existing orders remain accessible.',
                { title: lifecycle?.product.title || '' }
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <StoreError error={error} />
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy !== null}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              variant={lifecycle?.kind === 'delete' ? 'destructive' : 'default'}
              disabled={busy !== null}
              onClick={confirmLifecycle}
            >
              {t(
                busy !== null
                  ? 'Saving...'
                  : lifecycle?.kind === 'delete'
                    ? 'Delete product'
                    : 'Unlist product'
              )}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {editing !== null && (
        <StoreProductEditor
          key={editing === 'new' ? 'new' : editing.id}
          product={editing === 'new' ? undefined : editing}
          allowedMethods={allowedMethods}
          minimumPriceQuota={config.data?.minimum_unit_price_quota}
          linkPresets={config.data?.product_link_presets}
          testModeSupported={config.data?.product_test_mode_supported === true}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null)
            await Promise.all([
              client.invalidateQueries({
                queryKey: ['store', 'my-products', user.id],
              }),
              client.invalidateQueries({ queryKey: ['store', 'products'] }),
              ...(editing !== 'new'
                ? [
                    client.invalidateQueries({
                      queryKey: ['store', 'product', editing.id],
                    }),
                    client.invalidateQueries({
                      queryKey: [
                        'store',
                        'product-preview',
                        editing.id,
                        user.id,
                      ],
                    }),
                  ]
                : []),
            ])
          }}
        />
      )}
      {inventory && (
        <StoreInventoryImport
          key={inventory.id}
          product={
            query.data?.items.find((product) => product.id === inventory.id) ||
            inventory
          }
          onClose={() => setInventory(null)}
          onChanged={async () => {
            await Promise.all([
              client.invalidateQueries({
                queryKey: ['store', 'my-products', user.id],
              }),
              client.invalidateQueries({ queryKey: ['store', 'products'] }),
              client.invalidateQueries({
                queryKey: ['store', 'product', inventory.id],
              }),
              client.invalidateQueries({
                queryKey: ['store', 'product-preview', inventory.id, user.id],
              }),
            ])
          }}
          onSaved={async () => {
            setInventory(null)
            await Promise.all([
              client.invalidateQueries({
                queryKey: ['store', 'my-products', user.id],
              }),
              client.invalidateQueries({ queryKey: ['store', 'products'] }),
              client.invalidateQueries({
                queryKey: ['store', 'product', inventory.id],
              }),
              client.invalidateQueries({
                queryKey: ['store', 'product-preview', inventory.id, user.id],
              }),
            ])
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
  minimumPriceQuota,
  linkPresets = [],
  testModeSupported = false,
  onClose,
  onSaved,
}: {
  product?: StoreProduct
  allowedMethods: StorePaymentMethod[]
  minimumPriceQuota?: number
  linkPresets?: StoreLinkPreset[]
  testModeSupported?: boolean
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const { t, i18n } = useTranslation()
  const money = useWalletCurrency()
  const price = useStoreMoneyDraft(product?.price_quota ?? Number.NaN)
  const minimum =
    Number.isSafeInteger(minimumPriceQuota) && (minimumPriceQuota ?? -1) >= 0
      ? minimumPriceQuota
      : undefined
  const minimumAmount =
    minimum === undefined
      ? ''
      : formatMinimumQuotaInCurrency(
          minimum,
          price.currency,
          {
            locale: getCurrencyFormattingLocale(i18n.language),
            creditLabel: t('Credits'),
            abbreviate: false,
          },
          money.config
        )
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
      if (minimum === undefined) {
        throw new Error(t('Loading...'))
      }
      if (!price.quota || !draft.title.trim()) {
        throw new Error('Enter a valid title and price')
      }
      if (price.quota < minimum) {
        throw new Error(t(minimumCopy.error, { amount: minimumAmount }))
      }
      if (
        draft.payment_methods.some((method) => !allowedMethods.includes(method))
      ) {
        throw new Error(t(copy.unavailable))
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
      if (testModeSupported) body.test_mode = draft.test_mode === true
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
              {product?.variants && (
                <p className='text-muted-foreground text-xs leading-5'>
                  {t(
                    'Product price and template edits apply only to the default variant. Other variants are managed separately.'
                  )}
                </p>
              )}
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
              {price.quota === undefined && (
                <p className='text-destructive text-xs'>
                  {t('Invalid amount')}
                </p>
              )}
              {minimum !== undefined && minimum > 0 && (
                <p className='text-muted-foreground text-xs'>
                  {t(minimumCopy.current, { amount: minimumAmount })}
                </p>
              )}
              {minimum === undefined && (
                <p className='text-muted-foreground text-xs'>
                  {t('Loading...')}
                </p>
              )}
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
            <StoreLinkPresetChooser
              presets={linkPresets}
              onAdd={(link) => change('links', [...draft.links, link])}
            />
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
                {Object.entries(DELIVERY_TEMPLATES).map(([key, template]) => (
                  <option key={key} value={key}>
                    {t(template.label)}
                  </option>
                ))}
              </select>
              <p className='text-muted-foreground text-xs'>
                {t(DELIVERY_TEMPLATES[draft.template].help)}
              </p>
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
            <p className='text-muted-foreground text-sm'>{t(copy.subset)}</p>
            {[...new Set([...allowedMethods, ...draft.payment_methods])].map(
              (method) => (
                <div
                  key={method}
                  className='flex items-center justify-between gap-3 py-1 text-sm'
                >
                  <Label htmlFor={`store-product-payment-${method}`}>
                    {t(paymentLabel(method))}
                  </Label>
                  <Switch
                    id={`store-product-payment-${method}`}
                    checked={draft.payment_methods.includes(method)}
                    disabled={
                      !allowedMethods.includes(method) &&
                      !draft.payment_methods.includes(method)
                    }
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
                </div>
              )
            )}
            {draft.payment_methods.some(
              (method) => !allowedMethods.includes(method)
            ) && (
              <p role='status' className='text-warning text-sm'>
                {t(copy.unavailable)}
              </p>
            )}
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
            <label className='flex items-center justify-between gap-4 text-sm'>
              <span className='space-y-1'>
                <span className='block'>
                  {t('Require purchasing account to collect')}
                </span>
                <span className='text-muted-foreground block text-xs leading-5'>
                  {t(
                    'If your IP cannot sign in and you have no valid session, sign in from an allowed network first. For collection from any IP, sellers can turn off account-only collection and require a pickup code instead.'
                  )}
                </span>
              </span>
              <Switch
                checked={draft.pickup_login_required}
                onCheckedChange={(value) =>
                  change('pickup_login_required', value)
                }
              />
            </label>
            <p className='text-muted-foreground text-xs leading-5'>
              {t(
                'Buyers can always fill in these fields. The switches only make them required.'
              )}
            </p>
            {(
              [
                [
                  'pickup_code_required',
                  'Pickup code',
                  'store-pickup-code-preview',
                  'Require a pickup code',
                ],
                [
                  'email_pickup_link',
                  'Pickup email',
                  'store-pickup-email-preview',
                  'Require a pickup email',
                ],
              ] as const
            ).map(([key, label, id, requiredLabel]) => (
              <div key={key} className='space-y-2'>
                <Label htmlFor={id}>{t(label)}</Label>
                <div className='flex items-center gap-3'>
                  <Input
                    id={id}
                    readOnly
                    tabIndex={-1}
                    placeholder={t('Buyer enters this at checkout')}
                    className='h-11 min-w-0 flex-1'
                  />
                  <label className='flex shrink-0 items-center gap-2 text-xs'>
                    {t('Required field', { defaultValue: t('Required') })}
                    <Switch
                      aria-label={t(requiredLabel)}
                      checked={draft[key]}
                      onCheckedChange={(value) => change(key, value)}
                    />
                  </label>
                </div>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    key === 'pickup_code_required'
                      ? 'If filled in, this code protects collection. Use at least 8 characters and keep it safe.'
                      : 'If filled in, the pickup link will be sent to this email after payment.'
                  )}
                </p>
              </div>
            ))}
          </fieldset>
          {testModeSupported && (
            <div className='space-y-2 rounded-lg border p-4'>
              <label className='flex items-center justify-between gap-4 text-sm'>
                <span>{t(testCopy.label)}</span>
                <Switch
                  checked={draft.test_mode === true}
                  onCheckedChange={(value) => change('test_mode', value)}
                />
              </label>
              <p className='text-muted-foreground text-xs leading-5'>
                {t(testCopy.help)}
              </p>
              <p className='text-muted-foreground text-xs leading-5'>
                {t(testCopy.changed)}
              </p>
            </div>
          )}
          <div className='flex justify-end gap-2 border-t pt-4'>
            <Button
              type='button'
              variant='outline'
              disabled={busy}
              onClick={onClose}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='submit'
              disabled={
                busy || price.quota === undefined || minimum === undefined
              }
            >
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
  onChanged,
}: {
  product: StoreProduct
  onClose: () => void
  onSaved: () => Promise<void>
  onChanged?: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [variantId, setVariantId] = useState(
    product.default_variant_id ||
      product.variants?.find((variant) => variant.is_default)?.id ||
      ''
  )
  const [busy, setBusy] = useState(false)
  const [dirty, setDirty] = useState(false)
  const legacy = legacyVariantProduct(product)
  const variant = product.variants?.find(
    (item) => item.id === variantId && item.product_id === product.id
  )
  function selectVariant(id: string) {
    if (busy || id === variantId) return
    if (
      dirty &&
      !window.confirm(
        t('Switching variants clears the unsaved inventory draft. Continue?')
      )
    ) {
      return
    }
    setVariantId(id)
    setDirty(false)
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
        <Tabs
          value={variantId}
          onValueChange={(id) => selectVariant(String(id))}
        >
          {!legacy && (
            <div className='space-y-2'>
              <TabsList
                aria-label={t('Variant inventory')}
                variant='line'
                className='h-auto flex-wrap justify-start'
              >
                {product.variants?.map((item) => (
                  <TabsTrigger
                    key={item.id}
                    value={item.id}
                    disabled={busy}
                    className='min-h-11 break-words whitespace-normal'
                  >
                    {item.name || t('Default variant')}
                    {!item.enabled && (
                      <span className='text-muted-foreground ml-1 text-xs'>
                        ({t('Disabled')})
                      </span>
                    )}
                  </TabsTrigger>
                ))}
              </TabsList>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Imports go only to the selected variant. Disabled variants can still be restocked.'
                )}
              </p>
              {variant && (
                <StoreInventoryTotals product={product} variant={variant} />
              )}
            </div>
          )}
          {legacy || variant ? (
            <TabsContent value={variantId}>
              <StoreInventoryContent
                key={variant?.id || 'legacy-default'}
                product={product}
                variant={variant}
                onSaved={onSaved}
                onChanged={onChanged}
                onBusyChange={setBusy}
                onDirtyChange={setDirty}
              />
            </TabsContent>
          ) : (
            <p className='text-muted-foreground text-sm'>
              {t('Choose a variant before importing inventory.')}
            </p>
          )}
        </Tabs>
      </DialogContent>
    </Dialog>
  )
}
function StoreInventoryContent({
  product,
  variant,
  onSaved,
  onChanged,
  onBusyChange,
  onDirtyChange,
}: {
  product: StoreProduct
  variant?: StoreVariant
  onBusyChange: (value: boolean) => void
  onDirtyChange: (value: boolean) => void
  onSaved: () => Promise<void>
  onChanged?: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const user = useAuthStore((state) => state.auth.user)!
  const stock = useQuery({
    queryKey: [
      'store',
      'stock',
      user.id,
      product.id,
      variant?.id || 'legacy-default',
      page,
    ],
    queryFn: () =>
      variant
        ? storeApi.variantStock(product.id, variant.id, page)
        : storeApi.stock(product.id, page),
    retry: false,
  })
  const template = variant?.template ?? product.template
  const composed = template === 'custom-text' || structuredTemplate(template)
  const [composedItems, setComposedItems] = useState<string[]>([])
  useEffect(() => onBusyChange(busy), [busy, onBusyChange])
  useEffect(
    () => onDirtyChange(text.length > 0 || composedItems.length > 0),
    [text, composedItems, onDirtyChange]
  )
  let count = composed ? composedItems.length : 0
  try {
    if (!composed) count = parseInventoryText(text).length
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
      const items = composed ? composedItems : parseInventoryText(text)
      if (composed) validateComposedItems(items)
      if (!items.length) throw new Error('Add at least one inventory item')
      if (variant) {
        await storeApi.importVariantStock(product.id, variant.id, items)
      } else {
        await storeApi.inventory(product.id, items)
      }
      setText('')
      setComposedItems([])
      await onSaved()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className='space-y-4'>
      <DialogDescription>
        {product.title} ·{' '}
        {composed
          ? t(DELIVERY_TEMPLATES[template].help)
          : t(
              'One text item per line. Empty lines are ignored. Duplicate lines remain separate stock items.'
            )}
      </DialogDescription>
      <StoreError error={error} />
      {!composed && (
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
      )}
      {composed ? (
        <StoreInventoryComposer
          template={template}
          items={composedItems}
          onChange={setComposedItems}
          disabled={busy}
        />
      ) : (
        <Textarea
          rows={9}
          value={text}
          onChange={(event) => setText(event.target.value)}
          aria-label={t('Inventory text')}
          placeholder={t('One item per line')}
        />
      )}
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
                          void (
                            variant
                              ? storeApi.removeVariantStock(
                                  product.id,
                                  variant.id,
                                  item.id
                                )
                              : storeApi.removeStock(product.id, item.id)
                          )
                            .then(async () => {
                              await stock.refetch()
                              await onChanged?.()
                            })
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
    </div>
  )
}
