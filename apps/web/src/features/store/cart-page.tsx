/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { catalogueApi } from './catalogue-api'
import { useStoreCatalogueSupport } from './catalogue-support'
import { StoreCatalogueTags } from './catalogue-tags'
import type {
  GuestStoreCartRef,
  StoreCartItem,
  StoreCatalogueProduct,
} from './catalogue-types'
import { StoreCollectionActions } from './collection-actions'
import { STORE_COLLECTION_REASON_LABELS } from './collection-copy'
import {
  guestCartClear,
  guestCartRemove,
  guestCartUpsert,
  useGuestStoreCart,
} from './collection-storage'
import {
  storeCartCapacity,
  storeCartCheckoutUrl,
  storeCartVariantName,
} from './collection-utils'
import { StoreMerchantIdentity } from './merchant-identity'
import { storeQuantity } from './quantity'
import { StoreAmount, StoreError, StoreLoading } from './shared'
import { currentStoreViewer, useStoreViewer } from './store-viewer'
import { storeVariantPrice } from './variant-utils'

type CartDisplayItem = Omit<StoreCartItem, 'created_at' | 'updated_at'>

function assertViewer(viewer: string) {
  if (currentStoreViewer() !== viewer) {
    throw new Error(
      'Your account changed. Refresh this page before continuing.'
    )
  }
}

export function StoreCartPage() {
  const viewer = useStoreViewer()
  return <CartPage key={viewer} viewer={viewer} />
}

function CartPage({ viewer }: { viewer: string }) {
  const { t } = useTranslation()
  const support = useStoreCatalogueSupport()
  const client = useQueryClient()
  const guest = useGuestStoreCart()
  const account = viewer !== 'anonymous'
  const cart = useQuery({
    queryKey: ['store', 'cart', viewer],
    queryFn: ({ signal }) => catalogueApi.allCart(signal),
    enabled: account && support.collectionsSupported,
    retry: false,
  })
  const clear = useMutation({
    mutationFn: async () => {
      assertViewer(viewer)
      if (account) await catalogueApi.clearCart()
      else guestCartClear()
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['store', 'cart', viewer] })
    },
  })
  const transfer = useMutation({
    mutationFn: async () => {
      // Absolute quantities make retries safe. Matching SKUs keep the larger
      // quantity instead of adding the same anonymous cart a second time.
      for (const ref of guest) {
        assertViewer(viewer)
        const fresh = await catalogueApi.product(ref.product_id)
        assertViewer(viewer)
        const existing = (await catalogueApi.allCart()).find(
          (item) =>
            item.product_id === ref.product_id &&
            item.variant_id === ref.variant_id
        )
        assertViewer(viewer)
        const quantity = Math.max(existing?.quantity ?? 0, ref.quantity)
        if (quantity > storeCartCapacity(fresh, ref.variant_id)) {
          throw new Error(
            'This quantity is no longer available. Refresh the product and check its purchase limits.'
          )
        }
        await catalogueApi.putCart({ ...ref, quantity })
        assertViewer(viewer)
        guestCartRemove(ref.product_id, ref.variant_id)
      }
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: ['store', 'cart', viewer] })
    },
  })
  const refreshing = useMutation({
    mutationFn: async () => {
      assertViewer(viewer)
      await client.invalidateQueries({ queryKey: ['store', 'cart', viewer] })
      await client.invalidateQueries({
        queryKey: ['store', 'guest-cart-product', viewer],
      })
    },
  })
  return (
    <div className='space-y-5'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='space-y-1'>
          <h1 className='text-xl font-bold'>{t('Shopping cart')}</h1>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Each item is checked and paid for separately with its seller. The cart does not reserve stock.'
            )}
          </p>
        </div>
        <div className='flex flex-wrap gap-2'>
          <Button
            variant='outline'
            disabled={refreshing.isPending || !support.collectionsSupported}
            onClick={() => refreshing.mutate()}
          >
            {t('Refresh cart')}
          </Button>
          <Button
            variant='outline'
            disabled={
              clear.isPending ||
              transfer.isPending ||
              !support.collectionsSupported ||
              (account ? !cart.data?.length : !guest.length)
            }
            onClick={() => clear.mutate()}
          >
            {t('Clear cart')}
          </Button>
        </div>
      </div>
      <StoreError error={support.error} retry={() => void support.refetch()} />
      {support.isPending ? (
        <StoreLoading />
      ) : !support.collectionsSupported ? (
        <p role='status' className='text-muted-foreground'>
          {t('Cart and favorites are not supported by this shop server yet.')}
        </p>
      ) : (
        <>
          <StoreError error={clear.error || refreshing.error} />
          {account ? (
            <>
              <StoreError
                error={cart.error}
                retry={() => void cart.refetch()}
              />
              {cart.isPending ? (
                <StoreLoading />
              ) : (
                cart.data && <CartItems items={cart.data} viewer={viewer} />
              )}
              {guest.length > 0 && (
                <section
                  className='space-y-3 border-t pt-4'
                  aria-label={t('Saved guest cart')}
                >
                  <h2 className='font-semibold'>{t('Saved guest cart')}</h2>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'Move these items to this account. Matching variants keep the larger quantity.'
                    )}
                  </p>
                  <div className='flex flex-wrap gap-2'>
                    <Button
                      disabled={transfer.isPending || clear.isPending}
                      onClick={() => transfer.mutate()}
                    >
                      {t('Move guest cart to this account')}
                    </Button>
                    <Button
                      variant='ghost'
                      disabled={transfer.isPending}
                      onClick={guestCartClear}
                    >
                      {t('Discard saved guest cart')}
                    </Button>
                  </div>
                  <StoreError error={transfer.error} />
                  <GuestCartItems
                    refs={guest}
                    viewer={viewer}
                    disabled={transfer.isPending}
                  />
                </section>
              )}
            </>
          ) : (
            <GuestCartItems refs={guest} viewer={viewer} />
          )}
        </>
      )}
      <Button variant='ghost' render={<a href='/store' />}>
        {t('Continue browsing')}
      </Button>
    </div>
  )
}

function GuestCartItems({
  refs,
  viewer,
  disabled = false,
}: {
  refs: GuestStoreCartRef[]
  viewer: string
  disabled?: boolean
}) {
  const productIds = [...new Set(refs.map((ref) => ref.product_id))]
  const products = useQueries({
    queries: productIds.map((productId) => ({
      queryKey: ['store', 'guest-cart-product', viewer, productId],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        catalogueApi.product(productId, signal),
      retry: false,
      staleTime: 0,
    })),
  })
  const byId = new Map(productIds.map((id, index) => [id, products[index]]))
  const items: CartDisplayItem[] = refs.map((ref) => {
    const query = byId.get(ref.product_id)
    return {
      ...ref,
      id: `${ref.product_id}:${ref.variant_id}`,
      product: query?.data ?? null,
      valid:
        !!query?.data &&
        ref.quantity <= storeCartCapacity(query.data, ref.variant_id),
      unavailable_reason: query?.error ? 'unavailable' : null,
    }
  })
  return (
    <CartItems
      items={items}
      viewer={viewer}
      guest
      disabled={disabled}
      loading={products.some((query) => query.isPending)}
    />
  )
}

function CartItems({
  items,
  viewer,
  guest = false,
  disabled = false,
  loading = false,
}: {
  items: CartDisplayItem[]
  viewer: string
  guest?: boolean
  disabled?: boolean
  loading?: boolean
}) {
  const { t } = useTranslation()
  if (!items.length) {
    return (
      <p className='text-muted-foreground py-6'>{t('Your cart is empty.')}</p>
    )
  }
  // Each SKU remains a distinct row even when products or sellers match.
  const groups = new Map<number, CartDisplayItem[]>()
  for (const item of items) {
    const seller = item.product?.seller_id ?? 0
    groups.set(seller, [...(groups.get(seller) ?? []), item])
  }
  return (
    <div className='space-y-5'>
      {loading && <StoreLoading />}
      {[...groups].map(([sellerId, rows]) => (
        <section
          key={sellerId}
          className='space-y-3'
          aria-label={t('Seller cart items')}
        >
          {sellerId > 0 && (
            <StoreMerchantIdentity
              seller={rows[0].product?.seller}
              sellerId={sellerId}
            />
          )}
          <p className='text-muted-foreground text-xs'>
            {t(
              'Checkout is separate for each item. No combined payment is created.'
            )}
          </p>
          <div className='divide-y border-y'>
            {rows.map((item) => (
              <CartRow
                key={`${item.product_id}-${item.variant_id}-${item.quantity}`}
                item={item}
                viewer={viewer}
                guest={guest}
                disabled={disabled}
              />
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}

function CartRow({
  item,
  viewer,
  guest,
  disabled,
}: {
  item: CartDisplayItem
  viewer: string
  guest: boolean
  disabled: boolean
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [quantity, setQuantity] = useState(String(item.quantity))
  const [reviewState, setReviewState] = useState<{
    product: StoreCatalogueProduct
    basis: StoreCatalogueProduct | null
    valid: boolean
  } | null>(null)
  const value = storeQuantity(quantity)
  const product = item.product
  const reviewed =
    reviewState &&
    reviewState.basis === product &&
    reviewState.valid === item.valid
      ? reviewState.product
      : null
  const capacity = product ? storeCartCapacity(product, item.variant_id) : 0
  const unitPrice = product
    ? storeVariantPrice(product, item.variant_id)
    : undefined
  const update = useMutation({
    mutationFn: async (next: number | null) => {
      assertViewer(viewer)
      setReviewState(null)
      if (guest) {
        if (next === null) guestCartRemove(item.product_id, item.variant_id)
        else {
          guestCartUpsert({
            product_id: item.product_id,
            variant_id: item.variant_id,
            quantity: next,
          })
        }
      } else if (next === null) await catalogueApi.removeCart(item.id)
      else {
        await catalogueApi.putCart({
          product_id: item.product_id,
          variant_id: item.variant_id,
          quantity: next,
        })
      }
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['store', 'cart', viewer] })
    },
  })
  const review = useMutation({
    mutationFn: async () => {
      assertViewer(viewer)
      const fresh = await catalogueApi.product(item.product_id)
      assertViewer(viewer)
      if (item.quantity > storeCartCapacity(fresh, item.variant_id)) {
        throw new Error(
          'This quantity is no longer available. Refresh the product and check its purchase limits.'
        )
      }
      setReviewState({ product: fresh, basis: product, valid: item.valid })
    },
  })
  const price = reviewed
    ? storeVariantPrice(reviewed, item.variant_id)
    : unitPrice
  const title = (reviewed ?? product)?.title
  const target = storeCartCheckoutUrl(item)
  const requiresLogin =
    viewer === 'anonymous' && reviewed?.purchase_login_required !== false
  const locked = disabled || update.isPending || review.isPending
  return (
    <article className='grid gap-4 py-4 sm:grid-cols-[minmax(0,1fr)_15rem]'>
      <div className='min-w-0 space-y-2'>
        <h3 className='font-semibold'>
          {title ? (
            <a
              className='hover:underline'
              href={`/store/products/${encodeURIComponent(item.product_id)}`}
            >
              {title}
            </a>
          ) : (
            t('Unavailable product')
          )}
        </h3>
        {product && (
          <p className='text-muted-foreground text-sm'>
            {storeCartVariantName(product, item.variant_id) ??
              t('Unavailable variant')}
          </p>
        )}
        {product && <StoreCatalogueTags product={product} />}
        {price !== undefined && (
          <p className='text-sm'>
            {t('Current catalogue unit price')}: <StoreAmount quota={price} />
          </p>
        )}
        {price !== undefined && Number.isSafeInteger(price * item.quantity) && (
          <p className='text-sm'>
            {t('Catalogue item total')}:{' '}
            <StoreAmount quota={price * item.quantity} />
          </p>
        )}
        {(!item.valid || item.quantity > capacity) && (
          <p role='status' className='text-destructive text-sm'>
            {item.unavailable_reason &&
              `${t(STORE_COLLECTION_REASON_LABELS[item.unavailable_reason])}. `}
            {t(
              'This item is unavailable or exceeds the current stock or purchase limit. Refresh it before checkout.'
            )}
          </p>
        )}
        <StoreError error={update.error || review.error} />
        {reviewed && (
          <div className='bg-muted space-y-2 p-3 text-sm'>
            <p>
              {t(
                'Price and availability refreshed. Review the current price, then continue to the seller terms and payment.'
              )}
            </p>
            <Button
              size='sm'
              render={
                <a
                  href={
                    requiresLogin
                      ? `/sign-in?redirect=${encodeURIComponent(target)}`
                      : target
                  }
                />
              }
            >
              {t(
                requiresLogin
                  ? 'Sign in and continue checkout'
                  : 'Continue to checkout'
              )}
            </Button>
          </div>
        )}
      </div>
      <div className='space-y-3'>
        <div className='flex items-center gap-1'>
          <Button
            variant='outline'
            size='sm'
            className='size-11'
            aria-label={t('Decrease cart quantity')}
            disabled={locked || item.quantity <= 1}
            onClick={() => update.mutate(item.quantity - 1)}
          >
            −
          </Button>
          <Input
            aria-label={t('Cart quantity')}
            className='h-11 w-20 text-center tabular-nums'
            inputMode='numeric'
            value={quantity}
            onChange={(event) => setQuantity(event.target.value)}
            disabled={locked}
            maxLength={16}
          />
          <Button
            variant='outline'
            size='sm'
            className='size-11'
            aria-label={t('Increase cart quantity')}
            disabled={locked || item.quantity >= capacity}
            onClick={() => update.mutate(item.quantity + 1)}
          >
            +
          </Button>
          <Button
            variant='ghost'
            size='sm'
            disabled={
              locked ||
              value === undefined ||
              value > capacity ||
              value === item.quantity
            }
            onClick={() => {
              if (value !== undefined) update.mutate(value)
            }}
          >
            {t('Update')}
          </Button>
        </div>
        <p className='text-muted-foreground text-xs'>
          {t('Available to buy: {{count}}', { count: capacity })}
        </p>
        <div className='flex flex-wrap gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={
              locked || !product || item.quantity > capacity || !item.valid
            }
            onClick={() => review.mutate()}
          >
            {t('Review item for checkout')}
          </Button>
          <Button
            size='sm'
            variant='ghost'
            disabled={locked}
            onClick={() => update.mutate(null)}
          >
            {t('Remove')}
          </Button>
        </div>
        {product && (
          <StoreCollectionActions
            product={product}
            variantId={item.variant_id}
            quantity={item.quantity}
            cartAction={false}
          />
        )}
      </div>
    </article>
  )
}
