/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type SetStateAction } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Markdown } from '@/components/ui/markdown'
import { refreshCurrentAccount } from '@/features/onboarding/use-auth-user-refresh'
import { useAuthStore } from '@/stores/auth-store'

import { storeGuestApi } from './access-api'
import { STORE_ACCESS_COPY as accessCopy } from './access-copy'
import {
  storeAccessSupported,
  storeVisibility,
  storePurchaseLoginRequired,
} from './access-types'
import { StoreAPIError, storeApi } from './api'
import { catalogueApi } from './catalogue-api'
import { StoreCatalogueTags } from './catalogue-tags'
import type { StoreCatalogueProduct } from './catalogue-types'
import { storeCheckoutReturnUrl } from './checkout-intent'
import { useStoreCheckoutRecovery } from './checkout-recovery'
import { StoreCollectionActions } from './collection-actions'
import { StoreGuestEmailVerification } from './guest-email'
import { rememberStoreGuestOrder } from './guest-order-storage'
import { useStoreGuestSession, useStoreGuestDisclaimer } from './guest-session'
import { StoreMerchantIdentity } from './merchant-identity'
import { StoreMerchantTermsAcceptance } from './merchant-terms'
import { StoreProductPromotion } from './product-promotion'
import { STORE_PURCHASE_LIMIT_COPY as purchaseCopy } from './purchase-limits-copy'
import {
  storeCheckoutCapacity,
  storeClampQuantity,
  storeQuantity,
} from './quantity'
import { StoreQuantityControl } from './quantity-control'
import {
  StoreAmount,
  StoreAuthGate,
  StoreBadges,
  StoreError,
  StoreLoading,
} from './shared'
import { currentStoreViewer, useStoreViewer } from './store-viewer'
import { STORE_TEST_MODE_COPY as testCopy } from './test-mode-copy'
import type {
  StoreCheckoutResult,
  StorePaymentSession,
  StorePaymentMethod,
} from './types'
import { useStoreGuestEmailVerification } from './use-store-guest-email'
import { useStoreMerchantTerms } from './use-store-merchant-terms'
import { useStoreProductPromotion } from './use-store-product-promotion'
import {
  paymentLabel,
  continueStorePayment,
  safeStoreUrl,
  storeTotal,
  isStoreEmail,
} from './utils'
import {
  enabledStoreVariants,
  initialStoreVariant,
  legacyVariantProduct,
  selectedStoreVariant,
  storeVariantCapacity,
  storeVariantPrice,
} from './variant-utils'

export function StoreProductPage({
  id,
  ownerPreview = false,
  promotionCode = '',
  initialVariantId,
  initialQuantity = 1,
  cartItemId,
}: {
  id: string
  ownerPreview?: boolean
  promotionCode?: string
  initialVariantId?: string
  initialQuantity?: number
  cartItemId?: string
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const viewer = useStoreViewer()
  const validId = /^[a-zA-Z0-9-]{1,64}$/.test(id)
  const query = useQuery({
    queryKey: ownerPreview
      ? ['store', 'product-preview', viewer, id]
      : ['store', 'product', viewer, id],
    queryFn: ({ signal }) => {
      if (currentStoreViewer() !== viewer) throw new Error('Product not found')
      return ownerPreview
        ? storeApi.previewProduct(id, signal)
        : catalogueApi.product(id, signal)
    },
    enabled: validId && (!ownerPreview || !!user),
    retry: false,
  })
  if (ownerPreview && !user) return <StoreAuthGate>{null}</StoreAuthGate>
  if (!validId) return <StoreError error={new Error('Product not found')} />
  if (query.isPending) return <StoreLoading />
  if (!query.data) {
    return (
      <StoreError
        error={query.error || new Error('Product not found')}
        retry={() => void query.refetch()}
      />
    )
  }
  const product: StoreCatalogueProduct = query.data
  if (ownerPreview && product.seller_id !== user?.id) {
    return <StoreError error={new Error('Product not found')} />
  }
  return (
    <div className='space-y-5'>
      {ownerPreview && (
        <p className='rounded-lg border p-4 text-sm'>
          {t(testCopy.previewHelp)}
        </p>
      )}
      <a
        href='/store'
        className='text-muted-foreground text-sm hover:underline'
      >
        {t('Back to store')}
      </a>
      <div className='grid items-start gap-7 lg:grid-cols-[minmax(0,1fr)_22rem]'>
        <section className='min-w-0 space-y-5'>
          <div className='space-y-3'>
            <StoreBadges product={product} />
            <StoreCatalogueTags product={product} />
            <h1 className='console-page-title text-2xl font-bold'>
              {product.title}
            </h1>
            <StoreMerchantIdentity
              seller={product.seller}
              sellerId={product.seller_id}
            />
          </div>
          {product.image_urls?.filter((url) => safeStoreUrl(url)).length >
            0 && (
            <div className='grid gap-3 sm:grid-cols-2'>
              {product.image_urls
                .filter((url) => safeStoreUrl(url))
                .map((url, index) => (
                  <img
                    key={`${url}-${index}`}
                    src={safeStoreUrl(url)}
                    alt={product.title}
                    loading='lazy'
                    referrerPolicy='no-referrer'
                    className='aspect-[16/9] w-full rounded-lg border object-cover'
                  />
                ))}
            </div>
          )}
          <Markdown className='min-w-0'>{product.description}</Markdown>
          {product.contact && (
            <div className='space-y-1 border-t pt-4'>
              <h2 className='text-sm font-semibold'>{t('Seller contact')}</h2>
              <p className='text-muted-foreground text-sm break-words whitespace-pre-wrap'>
                {product.contact}
              </p>
            </div>
          )}
          {product.links?.length > 0 && (
            <div className='space-y-3 border-t pt-4'>
              <h2 className='text-sm font-semibold'>{t('Product links')}</h2>
              {product.links.map((link, index) => {
                const href = safeStoreUrl(link.url)
                return (
                  href && (
                    <a
                      key={index}
                      href={href}
                      target='_blank'
                      rel='noopener noreferrer'
                      className='hover:bg-muted block space-y-1 rounded-md p-2'
                    >
                      <strong className='text-sm underline underline-offset-4'>
                        {link.title || href}
                      </strong>
                      <p className='text-muted-foreground text-xs'>
                        {link.description}
                      </p>
                    </a>
                  )
                )
              })}
            </div>
          )}
        </section>
        <StoreCheckout
          key={`${product.id}-${viewer}-${promotionCode}-${initialVariantId || ''}-${initialQuantity}-${cartItemId || ''}`}
          product={product}
          ownerPreview={ownerPreview}
          promotionCode={promotionCode}
          initialVariantId={initialVariantId}
          initialQuantity={initialQuantity}
          cartItemId={cartItemId}
        />
      </div>
    </div>
  )
}

export function StoreCheckout({
  product: initialProduct,
  ownerPreview = false,
  initialVariantId,
  initialQuantity = 1,
  cartItemId,
  promotionCode: initialPromotionCode = '',
}: {
  product: StoreCatalogueProduct
  ownerPreview?: boolean
  promotionCode?: string
  initialVariantId?: string
  initialQuantity?: number
  cartItemId?: string
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const client = useQueryClient()
  const config = useQuery({
    queryKey: ['store', 'config'],
    queryFn: storeApi.config,
    retry: false,
  })
  const accessSupported = storeAccessSupported(config.data)
  const guestAllowed =
    accessSupported &&
    !user &&
    storeVisibility(initialProduct) === 'public' &&
    !storePurchaseLoginRequired(initialProduct) &&
    !initialProduct.pickup_login_required
  const guest = useStoreGuestSession(guestAllowed)
  const [guestDetail, setGuestDetail] = useState<{
    actor: string
    product: StoreCatalogueProduct
  } | null>(null)
  const [guestDetailError, setGuestDetailError] = useState<unknown>(null)
  const [guestDetailRevision, setGuestDetailRevision] = useState(0)
  useEffect(() => {
    setGuestDetail(null)
    setGuestDetailError(null)
    if (!guest.session) return
    const captured = guest.session
    const controller = new AbortController()
    void storeGuestApi
      .product(captured.token, initialProduct.id, controller.signal)
      .then((fresh) => {
        if (!controller.signal.aborted && !useAuthStore.getState().auth.user) {
          setGuestDetail({ actor: captured.guest_id, product: fresh })
        }
      })
      .catch((issue) => {
        if (!controller.signal.aborted) setGuestDetailError(issue)
      })
    return () => controller.abort()
  }, [guest.session, initialProduct, guestDetailRevision])
  const product =
    guest.session && guestDetail?.actor === guest.session.guest_id
      ? guestDetail.product
      : initialProduct
  const recovery = useStoreCheckoutRecovery({
    productId: product.id,
    supported: accessSupported,
    userId: user?.id,
    guest: guest.session,
  })
  const guestDisclaimer = useStoreGuestDisclaimer(guest.session?.token)
  const memberDisclaimer = useQuery({
    queryKey: ['store', 'disclaimer', user?.id],
    queryFn: storeApi.disclaimer,
    enabled: !guestAllowed,
    retry: false,
  })
  const disclaimer = guestAllowed ? guestDisclaimer : memberDisclaimer
  const sellerTerms = useStoreMerchantTerms(
    product.id,
    accessSupported,
    user?.id,
    guest.session?.token
  )
  const [quantity, setQuantity] = useState(() =>
    String(
      Number.isSafeInteger(initialQuantity) && initialQuantity > 0
        ? initialQuantity
        : 1
    )
  )
  // A deep link names a specific SKU. Only an unspecified SKU may default.
  const [variantId, setVariantId] = useState(
    () => initialVariantId || initialStoreVariant(product)
  )
  const selectedVariant = selectedStoreVariant(product, variantId)
  const variantCapacity = storeVariantCapacity(product, variantId)
  const unitPrice = storeVariantPrice(product, variantId)
  const [method, setMethod] = useState<StorePaymentMethod | ''>('')
  const [promotionCode, setPromotionCode] = useState(initialPromotionCode)
  const [code, setCode] = useState('')
  const [email, setEmail] = useState('')
  const guestEmail = useStoreGuestEmailVerification(
    guest.session?.token,
    email.trim()
  )
  const [open, setOpen] = useState(false)
  const [acknowledged, setAcknowledged] = useState(false)
  const [read, setRead] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [payment, setPayment] = useState<StorePaymentSession | null>(null)
  const actorScope = user ? `account:${user.id}` : guest.actorScope
  const [resultActor, setResultActor] = useState('')
  const [savedResult, setSavedResult] = useState<StoreCheckoutResult | null>(
    null
  )
  const result = resultActor === actorScope ? savedResult : null
  function setResult(value: SetStateAction<StoreCheckoutResult | null>) {
    setResultActor(actorScope)
    setSavedResult(value)
  }
  useEffect(() => {
    setSavedResult(null)
    setPayment(null)
    setCode('')
    setEmail('')
    setError(null)
  }, [actorScope])
  useEffect(() => {
    if (recovery.order) {
      setResultActor(actorScope)
      setSavedResult({ order: recovery.order, created: false })
      setGuestDetailRevision((current) => current + 1)
      if (guest.session) {
        try {
          rememberStoreGuestOrder(guest.session.guest_id, recovery.order.id)
        } catch {
          setError(new Error(accessCopy.orderReceiptFailed))
        }
      }
      if (
        recovery.order.status === 'paid' &&
        user?.id === recovery.order.buyer_id &&
        useAuthStore.getState().auth.user?.id === user.id
      ) {
        void refreshCurrentAccount()
        void client.invalidateQueries({
          queryKey: ['store', 'payments', user.id],
        })
      }
    }
  }, [recovery.order, actorScope, user?.id, client, guest.session])
  const recoveryBusy = busy || recovery.loading
  const settled =
    recovery.record?.state === 'known' && !!recovery.record.orderId
  useEffect(() => {
    setRead(false)
    setAcknowledged(false)
  }, [disclaimer.data?.version])
  const count = storeQuantity(quantity)
  const promotion = useStoreProductPromotion({
    product,
    variantId,
    quantity: count,
    code: promotionCode,
    userId: user?.id,
    guestToken: guest.session?.token,
    guestActorScope: guest.actorScope,
  })
  const free = promotion.quote?.free === true
  const promotionEligible = promotion.quote?.checkout_allowed === true
  const availablePaymentMethods = promotion.supplied
    ? promotion.quote?.payment_methods.filter(
        (item): item is StorePaymentMethod => item !== 'free'
      ) || []
    : product.payment_methods || []
  const paymentMethods = guestAllowed
    ? availablePaymentMethods.filter((item) => item !== 'balance')
    : availablePaymentMethods
  const actualMethod = free
    ? 'free'
    : paymentMethods.includes(method as StorePaymentMethod)
      ? method
      : paymentMethods[0] || ''
  useEffect(() => {
    const scoped = promotion.resolved?.variant_ids
    if (
      !variantId &&
      scoped?.length === 1 &&
      selectedStoreVariant(product, scoped[0])
    ) {
      setVariantId(scoped[0])
    }
  }, [variantId, promotion.resolved, product])
  // Product availability uses the original seller fee. Promotion quotes check
  // the discounted fee, stock, sales and buyer limit together on the server.
  const capacity = promotion.supplied
    ? Math.min(
        promotion.quote?.max_quantity ?? 0,
        free || actualMethod === 'balance' ? 1000 : 100
      )
    : storeCheckoutCapacity(
        {
          ...product,
          available_stock: variantCapacity,
          price_quota: unitPrice ?? 0,
        },
        actualMethod === 'free' ? '' : actualMethod
      )
  // Promotion quotes are tied to the chosen quantity. Keep that selection
  // while quoting; the trusted maximum disables an invalid purchase.
  useEffect(() => {
    if (promotion.supplied) return
    setQuantity((current) => storeClampQuantity(current, capacity))
  }, [capacity, promotion.supplied, promotion.quote])
  let total: number | undefined
  try {
    if (count !== undefined && unitPrice !== undefined) {
      total = promotion.supplied
        ? promotion.quote?.price_quota
        : storeTotal(unitPrice, count)
    }
  } catch {
    /* invalid input remains disabled */
  }
  const disclaimerNeeded = !product.official && !disclaimer.data?.accepted
  const pickupEmail = email.trim()
  const codeValid =
    code.length >= 8 && new TextEncoder().encode(code).length <= 72
  const emailValid = isStoreEmail(pickupEmail)
  const valid =
    (!!user || (!!guestAllowed && !!guest.session && !guest.loading)) &&
    (!initialVariantId || !!selectedVariant) &&
    (!guestAllowed ||
      (actualMethod !== 'balance' && guestEmail.ready && !!guestDetail)) &&
    total !== undefined &&
    (free || total > 0) &&
    count !== undefined &&
    count <= capacity &&
    (!product.trading_paused || promotionEligible) &&
    (storeVisibility(product) === 'private'
      ? product.seller_id === user?.id &&
        ['draft', 'pending', 'published'].includes(product.status)
      : product.status === 'published') &&
    !!actualMethod &&
    (free || paymentMethods.includes(actualMethod as StorePaymentMethod)) &&
    (!promotion.supplied || promotion.quote?.checkout_allowed === true) &&
    (code ? codeValid : !product.pickup_code_required) &&
    (pickupEmail ? emailValid : !product.email_pickup_link)
  async function checkout(accept = false, retry = false) {
    if (
      (!retry && !valid) ||
      recoveryBusy ||
      (!user && !guest.session) ||
      count === undefined
    ) {
      return
    }
    setBusy(true)
    setError(null)
    try {
      if (
        (disclaimerNeeded || (accessSupported && !sellerTerms.ready)) &&
        !accept &&
        !retry
      ) {
        setOpen(true)
        return
      }
      if (accessSupported && !sellerTerms.ready && !retry) return
      const version = disclaimer.data?.version
      if (disclaimerNeeded && !retry) {
        if (!version || !acknowledged || !read) return
        if (guest.session) {
          await storeGuestApi.acceptDisclaimer(guest.session.token, version)
          guestDisclaimer.accepted()
        } else {
          const auth = useAuthStore.getState().auth
          if (auth.user?.id !== user?.id) return
          await storeApi.acceptDisclaimer(version, {
            userId: user?.id,
            sessionId: auth.session?.sid,
          })
        }
        if (user) {
          client.setQueryData(['store', 'disclaimer', user?.id], {
            ...disclaimer.data,
            accepted: true,
          })
        }
        setOpen(false)
      }
      const body = {
        product_id: product.id,
        ...(!legacyVariantProduct(product) ? { variant_id: variantId } : {}),
        quantity: count,
        payment_method: actualMethod as StorePaymentMethod | 'free',
        ...(promotion.quote
          ? { promotion_code: promotion.quote.promotion_code }
          : {}),
        ...(accessSupported && sellerTerms.terms
          ? {
              seller_terms_version: sellerTerms.terms.version,
              accept_seller_terms: sellerTerms.accepted,
            }
          : {}),
        ...(!product.official && version
          ? { disclaimer_version: version }
          : {}),
        ...(code ? { pickup_code: code } : {}),
        ...(pickupEmail ? { pickup_email: pickupEmail } : {}),
      }
      const created = await recovery.submit(
        {
          productId: product.id,
          ...(!legacyVariantProduct(product) ? { variantId } : {}),
          quantity: count,
          ...(promotion.quote
            ? { promotionCode: promotion.quote.promotion_code }
            : {}),
          ...(cartItemId ? { cartItemId } : {}),
          ...(ownerPreview ? { ownerPreview: true } : {}),
        },
        body,
        retry
      )
      if (!created) return
      setOpen(false)
      setResult(created)
      if (guest.session) {
        rememberStoreGuestOrder(guest.session.guest_id, created.order.id)
      }
      setCode('')
      setEmail('')
      await client.invalidateQueries({
        queryKey: ['store', 'orders', user?.id],
      })
      await client.invalidateQueries({
        queryKey: ['store', 'product'],
      })
      await client.invalidateQueries({
        queryKey: ['store', 'product-preview'],
      })
    } catch (issue) {
      setError(issue)
      if (
        accessSupported &&
        issue instanceof StoreAPIError &&
        issue.code?.includes('TERMS')
      ) {
        sellerTerms.setAccepted(false)
        await sellerTerms.refresh()
      }
    } finally {
      setBusy(false)
    }
  }
  const orderApi = { pay: recovery.pay, order: recovery.refreshOrder }
  const resultNeedsPayment =
    result?.order.status === 'pending' &&
    result.order.payment_method !== 'free' &&
    result.order.price_quota > 0
  async function paymentError(issue: unknown) {
    setError(issue)
    if (
      issue instanceof StoreAPIError &&
      issue.code === 'STORE_PAYMENT_MINIMUM' &&
      issue.orderCancelled === true &&
      issue.orderStatus === 'cancelled' &&
      issue.orderId === result?.order.id
    ) {
      // Only the server can confirm that no payment obligation was issued.
      // A fresh quote must reclaim the released promotion use and buyer limits.
      try {
        await recovery.minimumCancellation(issue)
      } catch (failure) {
        setError(failure)
        return
      }
      setResult((current) =>
        current
          ? { ...current, order: { ...current.order, status: 'cancelled' } }
          : current
      )
      setPayment(null)
      setMethod(paymentMethods.includes('balance') ? 'balance' : '')
      promotion.refresh()
      void client.invalidateQueries({ queryKey: ['store', 'orders', user?.id] })
    }
  }
  return (
    <aside className='bg-card space-y-4 rounded-lg border p-5 lg:sticky lg:top-24'>
      <div className='space-y-1'>
        <div className='text-xl font-semibold'>
          {(result?.order.unit_price_quota ?? unitPrice) === undefined ? (
            '—'
          ) : (
            <StoreAmount
              quota={(result?.order.unit_price_quota ?? unitPrice)!}
            />
          )}
        </div>
        <p className='text-muted-foreground text-xs'>
          {t('Unit price')} ·{' '}
          {t('Stock: {{count}}', {
            count: promotion.quote ? capacity : variantCapacity,
          })}
        </p>
      </div>
      <StoreError error={error || recovery.error || guestDetailError} />
      {guestAllowed && (
        <StoreError error={guest.error} retry={() => void guest.retry()} />
      )}
      {product.trading_paused && !promotionEligible && (
        <p className='text-muted-foreground text-sm'>
          {t('This product or payment method is currently unavailable.')}
        </p>
      )}
      {result ? (
        <div className='space-y-3 border-t pt-4'>
          <h2 className='font-semibold'>
            {t(
              result.order.status === 'paid'
                ? 'Order paid'
                : result.order.status === 'pending'
                  ? 'Order created'
                  : result.order.status
            )}
          </h2>
          <p className='text-muted-foreground text-sm'>
            {result.order.variant_name || t('Historic/default variant')}
          </p>
          <p className='text-muted-foreground text-xs break-all'>
            {result.order.trade_no}
          </p>
          {resultNeedsPayment && !payment && (
            <Button
              className='w-full'
              disabled={busy}
              onClick={() => {
                setBusy(true)
                void orderApi
                  .pay(result.order.id)
                  .then(async (session) => {
                    setPayment(session)
                    if (session.status !== 'pending') {
                      const full = await orderApi.order(result.order.id)
                      setResult({ order: full, created: false })
                    }
                  })
                  .catch(paymentError)
                  .finally(() => setBusy(false))
              }}
            >
              {t(
                result.order.payment_method === 'balance'
                  ? 'Pay with balance'
                  : 'Prepare payment'
              )}
            </Button>
          )}
          {resultNeedsPayment && payment?.status === 'pending' && (
            <div className='space-y-3'>
              <p className='text-sm'>
                {t('Actual payment')}:{' '}
                <strong>
                  {payment.amount} {payment.currency}
                </strong>
              </p>
              <Button
                className='w-full'
                disabled={busy}
                onClick={() => {
                  if (busy) return
                  setBusy(true)
                  setError(null)
                  void orderApi
                    .pay(result.order.id, payment.currency)
                    .then(async (current) => {
                      setPayment(current.status === 'pending' ? current : null)
                      if (current.status !== 'pending') {
                        const authoritative = await orderApi.order(
                          result.order.id
                        )
                        setResult((previous) =>
                          previous
                            ? { ...previous, order: authoritative }
                            : previous
                        )
                        await client.invalidateQueries({
                          queryKey: ['store', 'orders', user?.id],
                        })
                        return
                      }
                      // Revalidate the frozen session before leaving; changed quotes need another click.
                      if (
                        current.amount_minor !== payment.amount_minor ||
                        current.currency !== payment.currency
                      ) {
                        return
                      }
                      continueStorePayment(current)
                    })
                    .catch(paymentError)
                    .finally(() => setBusy(false))
                }}
              >
                {t('Continue to payment')}
              </Button>
            </div>
          )}
          {settled && (
            <div className='flex flex-wrap gap-2'>
              <Button
                variant='outline'
                disabled={recoveryBusy}
                onClick={() => {
                  setBusy(true)
                  void recovery
                    .clear(true)
                    .then(() => {
                      setResult(null)
                      setPayment(null)
                      sellerTerms.setAccepted(false)
                      promotion.refresh()
                    })
                    .catch(setError)
                    .finally(() => setBusy(false))
                }}
              >
                {t(accessCopy.buyAgain)}
              </Button>
              <Button
                variant='ghost'
                disabled={recoveryBusy}
                onClick={() => {
                  setBusy(true)
                  void recovery
                    .clear(false)
                    .then(() => {
                      setResult(null)
                      setPayment(null)
                    })
                    .catch(setError)
                    .finally(() => setBusy(false))
                }}
              >
                {t(accessCopy.orderClearCompleted)}
              </Button>
            </div>
          )}
          <Button
            variant='outline'
            className='w-full'
            render={<a href='/store/orders' />}
          >
            {t('View order and collect items')}
          </Button>
        </div>
      ) : (
        <>
          <StoreProductPromotion
            promotion={promotion}
            onRemove={() => setPromotionCode('')}
          />
          {!legacyVariantProduct(product) && (
            <fieldset className='space-y-2'>
              <legend className='text-sm font-medium'>
                {t('Product variant')}
              </legend>
              {!enabledStoreVariants(product).length && (
                <p className='text-muted-foreground text-sm'>
                  {t('No variants are currently available.')}
                </p>
              )}
              {enabledStoreVariants(product).map((variant) => (
                <label
                  key={variant.id}
                  className='has-[:checked]:border-primary flex items-start gap-2 rounded-md border p-3 text-sm'
                >
                  <input
                    type='radio'
                    name='store-variant'
                    value={variant.id}
                    checked={variantId === variant.id}
                    onChange={() => setVariantId(variant.id)}
                    disabled={
                      !(
                        promotion.resolved &&
                        (promotion.resolved.variant_ids.length === 0 ||
                          promotion.resolved.variant_ids.includes(variant.id))
                      ) &&
                      (variant.trading_paused || variant.sale_available <= 0)
                    }
                  />
                  <span className='min-w-0 flex-1 break-words'>
                    {variant.name || t('Default variant')}
                    <span className='text-muted-foreground block text-xs'>
                      {t('Available to buy: {{count}}', {
                        count:
                          promotion.quote?.variant_id === variant.id
                            ? promotion.quote.max_quantity
                            : variant.sale_available,
                      })}
                    </span>
                  </span>
                  <StoreAmount quota={variant.price_quota} />
                </label>
              ))}
              {!selectedVariant && enabledStoreVariants(product).length > 0 && (
                <p className='text-muted-foreground text-xs'>
                  {t('Choose a variant before ordering.')}
                </p>
              )}
            </fieldset>
          )}
          <StoreQuantityControl
            value={quantity}
            max={capacity}
            disabled={
              busy ||
              capacity <= 0 ||
              (product.trading_paused && !promotionEligible)
            }
            onChange={setQuantity}
          />
          <StoreCollectionActions
            product={product}
            variantId={variantId}
            quantity={count || 1}
          />
          {product.max_quantity_per_order != null && (
            <p className='text-muted-foreground text-xs'>
              {t(purchaseCopy.orderSummary, {
                count: product.max_quantity_per_order,
              })}
            </p>
          )}
          {product.buyer_purchase_remaining != null && (
            <p className='text-muted-foreground text-xs'>
              {t(purchaseCopy.buyerSummary, {
                count: product.buyer_purchase_remaining,
              })}
            </p>
          )}
          {!free && (
            <fieldset className='space-y-2'>
              <legend className='mb-2 text-sm font-medium'>
                {t('Payment method')}
              </legend>
              {paymentMethods.map((item) => (
                <label
                  key={item}
                  className='has-[:checked]:border-primary flex cursor-pointer items-center gap-2 rounded-md border p-3 text-sm'
                >
                  <input
                    type='radio'
                    name='store-payment'
                    value={item}
                    checked={actualMethod === item}
                    onChange={() => setMethod(item)}
                  />
                  {t(paymentLabel(item))}
                </label>
              ))}
              {!paymentMethods.length && !promotion.supplied && (
                <p className='text-muted-foreground text-sm'>
                  {t('This seller has no available payment method.')}
                </p>
              )}
            </fieldset>
          )}
          <div className='space-y-2'>
            <div className='flex items-center justify-between gap-2'>
              <Label htmlFor='store-pickup-code'>{t('Pickup code')}</Label>
              <span className='text-muted-foreground text-xs'>
                {product.pickup_code_required
                  ? t('Required field', { defaultValue: t('Required') })
                  : t('Optional')}
              </span>
            </div>
            <Input
              id='store-pickup-code'
              className='h-11'
              type='password'
              autoComplete='new-password'
              value={code}
              minLength={8}
              maxLength={64}
              required={product.pickup_code_required}
              aria-describedby='store-pickup-code-help'
              aria-invalid={!!code && !codeValid}
              onChange={(event) => setCode(event.target.value)}
            />
            <p
              id='store-pickup-code-help'
              className='text-muted-foreground text-xs'
            >
              {t(
                'If filled in, this code protects collection. Use at least 8 characters and keep it safe.'
              )}
            </p>
            {new TextEncoder().encode(code).length > 72 && (
              <p role='alert' className='text-destructive text-xs'>
                {t('Pickup code is too long. Please shorten it and try again.')}
              </p>
            )}
          </div>
          <div className='space-y-2'>
            <div className='flex items-center justify-between gap-2'>
              <Label htmlFor='store-pickup-email'>{t('Pickup email')}</Label>
              <span className='text-muted-foreground text-xs'>
                {product.email_pickup_link
                  ? t('Required field', { defaultValue: t('Required') })
                  : t('Optional')}
              </span>
            </div>
            <Input
              id='store-pickup-email'
              className='h-11'
              type='email'
              autoComplete='email'
              value={email}
              maxLength={254}
              required={product.email_pickup_link}
              aria-describedby='store-pickup-email-help'
              aria-invalid={!!pickupEmail && !emailValid}
              onChange={(event) => setEmail(event.target.value)}
            />
            <p
              id='store-pickup-email-help'
              className='text-muted-foreground text-xs'
            >
              {t(
                'If filled in, the pickup link will be sent to this email after payment.'
              )}
            </p>
          </div>
          {guestAllowed && (
            <StoreGuestEmailVerification
              state={guestEmail}
              email={pickupEmail}
            />
          )}
          {accessSupported && sellerTerms.error && (
            <StoreError
              error={sellerTerms.error}
              retry={() => void sellerTerms.refresh()}
            />
          )}
          <div className='flex justify-between border-t pt-4 text-sm'>
            <span>{t('Total')}</span>
            <strong>
              {total === undefined ? '—' : <StoreAmount quota={total} />}
            </strong>
          </div>
          {recovery.record && !result && (
            <div className='space-y-2 rounded-md border p-3 text-sm'>
              <p>{t(accessCopy.orderUnknown)}</p>
              <Button
                variant='outline'
                disabled={recoveryBusy}
                onClick={() => {
                  setBusy(true)
                  setError(null)
                  void recovery
                    .recover()
                    .catch(setError)
                    .finally(() => setBusy(false))
                }}
              >
                {t(accessCopy.orderCheck)}
              </Button>
              {recovery.record.state === 'unknown' && (
                <Button
                  variant='outline'
                  disabled={recoveryBusy}
                  onClick={() => void checkout(false, true)}
                >
                  {t(accessCopy.orderRetry)}
                </Button>
              )}
            </div>
          )}
          {user || guestAllowed ? (
            <Button
              className='w-full'
              disabled={
                !valid ||
                recoveryBusy ||
                !!recovery.record ||
                (disclaimerNeeded && !disclaimer.data) ||
                (accessSupported &&
                  (!sellerTerms.terms?.configured || sellerTerms.loading))
              }
              onClick={() => void checkout()}
            >
              {t(
                busy ? 'Creating order...' : free ? 'Free claim' : 'Place order'
              )}
            </Button>
          ) : (
            <Button
              className='w-full'
              render={
                <a
                  href={`/sign-in?redirect=${encodeURIComponent(storeCheckoutReturnUrl({ productId: product.id, ...(variantId ? { variantId } : {}), quantity: count || 1, ...(promotion.supplied ? { promotionCode: promotion.supplied } : {}), ...(cartItemId ? { cartItemId } : {}), ...(ownerPreview ? { ownerPreview: true } : {}) }))}`}
                />
              }
            >
              {t('Sign in to buy')}
            </Button>
          )}
          {(!product.official || accessSupported) && (
            <>
              <Button
                variant='ghost'
                size='sm'
                className='w-full'
                onClick={() => {
                  setOpen(true)
                  setAcknowledged(false)
                  setRead(false)
                }}
              >
                {t(
                  accessSupported
                    ? accessCopy.readTerms
                    : 'Read purchase disclaimer'
                )}
              </Button>
              <StoreError
                error={disclaimer.error}
                retry={() => void disclaimer.refetch()}
              />
            </>
          )}
          <p className='text-muted-foreground text-xs'>
            {t(
              product.pickup_login_required
                ? 'Only the purchasing account can collect this order.'
                : 'Anyone with the pickup link and the pickup code, if set, can collect this order.'
            )}
          </p>
        </>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='sm:max-w-xl'>
          <DialogTitle>
            {t(
              accessSupported
                ? accessCopy.purchaseTerms
                : 'Non-official product disclaimer'
            )}
          </DialogTitle>
          <StoreError error={error} />
          <DialogDescription>
            {t(
              product.official && accessSupported
                ? accessCopy.termsHelp
                : 'Read the complete disclaimer before your first purchase from an independent seller.'
            )}
          </DialogDescription>
          {!product.official && (
            <div
              tabIndex={0}
              className='max-h-64 overflow-y-auto rounded-md border p-4 text-sm leading-7 whitespace-pre-wrap'
              onScroll={(event) => {
                const node = event.currentTarget
                if (
                  node.scrollTop + node.clientHeight >=
                  node.scrollHeight - 8
                ) {
                  setRead(true)
                }
              }}
              ref={(node) => {
                if (
                  node &&
                  disclaimer.data &&
                  node.scrollHeight <= node.clientHeight + 8 &&
                  !read
                ) {
                  setRead(true)
                }
              }}
            >
              {disclaimer.data?.version === 'merchant-store-v1'
                ? t('Store purchase disclaimer v1')
                : disclaimer.data?.text || t('Loading...')}
            </div>
          )}
          {accessSupported && <StoreMerchantTermsAcceptance {...sellerTerms} />}
          {(user || guest.session) && (disclaimerNeeded || accessSupported) && (
            <>
              {disclaimerNeeded && (
                <Field orientation='horizontal' className='items-start'>
                  <Checkbox
                    id='store-platform-disclaimer-accepted'
                    checked={acknowledged}
                    onCheckedChange={(value) => setAcknowledged(value === true)}
                    disabled={!read || !disclaimer.data}
                  />
                  <FieldLabel htmlFor='store-platform-disclaimer-accepted'>
                    {t('I have carefully read and agree to this disclaimer.')}
                  </FieldLabel>
                </Field>
              )}
              <Button
                disabled={
                  (disclaimerNeeded && (!read || !acknowledged)) ||
                  !valid ||
                  busy ||
                  !sellerTerms.ready
                }
                onClick={() => void checkout(true)}
              >
                {t('Agree and place order')}
              </Button>
            </>
          )}
        </DialogContent>
      </Dialog>
    </aside>
  )
}
