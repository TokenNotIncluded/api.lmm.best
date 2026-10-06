/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useAuthStore } from '@/stores/auth-store'

import { storeApi } from './api'
import { storeCheckoutCapacity, storeQuantity } from './quantity'
import { StoreQuantityControl } from './quantity-control'
import {
  StoreAmount,
  StoreAuthGate,
  StoreBadges,
  StoreError,
  StoreLoading,
} from './shared'
import { STORE_TEST_MODE_COPY as testCopy } from './test-mode-copy'
import type {
  StoreCheckoutResult,
  StorePaymentSession,
  StorePaymentMethod,
  StoreProduct,
} from './types'
import {
  paymentLabel,
  continueStorePayment,
  safeStoreUrl,
  storeRequestKey,
  storeTotal,
  isStoreEmail,
} from './utils'

export function StoreProductPage({
  id,
  ownerPreview = false,
}: {
  id: string
  ownerPreview?: boolean
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const validId = /^[a-zA-Z0-9-]{1,64}$/.test(id)
  const query = useQuery({
    queryKey: ownerPreview
      ? ['store', 'product-preview', id, user?.id]
      : ['store', 'product', id],
    queryFn: () =>
      ownerPreview ? storeApi.previewProduct(id) : storeApi.product(id),
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
  const product = query.data
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
            <h1 className='console-page-title text-2xl font-bold'>
              {product.title}
            </h1>
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
          <p className='max-w-prose text-sm leading-7 break-words whitespace-pre-wrap'>
            {product.description}
          </p>
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
          key={`${product.id}-${user?.id || 'guest'}`}
          product={product}
          ownerPreview={ownerPreview}
        />
      </div>
    </div>
  )
}

export function StoreCheckout({
  product,
  ownerPreview = false,
}: {
  product: StoreProduct
  ownerPreview?: boolean
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const client = useQueryClient()
  const disclaimer = useQuery({
    queryKey: ['store', 'disclaimer', user?.id],
    queryFn: storeApi.disclaimer,
    retry: false,
  })
  const [quantity, setQuantity] = useState('1')
  const [method, setMethod] = useState<StorePaymentMethod | ''>('')
  const [code, setCode] = useState('')
  const [email, setEmail] = useState('')
  const [open, setOpen] = useState(false)
  const [acknowledged, setAcknowledged] = useState(false)
  const [read, setRead] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [payment, setPayment] = useState<StorePaymentSession | null>(null)
  const [result, setResult] = useState<StoreCheckoutResult | null>(null)
  const keys = useRef(new Map<string, string>())
  useEffect(() => {
    setRead(false)
    setAcknowledged(false)
  }, [disclaimer.data?.version])
  const actualMethod = method || product.payment_methods?.[0] || ''
  const count = storeQuantity(quantity)
  const capacity = storeCheckoutCapacity(product, actualMethod)
  let total: number | undefined
  try {
    if (count !== undefined) total = storeTotal(product.price_quota, count)
  } catch {
    /* invalid input remains disabled */
  }
  const disclaimerNeeded = !product.official && !disclaimer.data?.accepted
  const pickupEmail = email.trim()
  const codeValid =
    code.length >= 8 && new TextEncoder().encode(code).length <= 72
  const emailValid = isStoreEmail(pickupEmail)
  const valid =
    !!user &&
    total !== undefined &&
    count !== undefined &&
    count <= capacity &&
    !product.trading_paused &&
    (product.test_mode === true
      ? ownerPreview &&
        product.seller_id === user?.id &&
        ['draft', 'pending', 'published'].includes(product.status)
      : product.status === 'published') &&
    !!actualMethod &&
    product.payment_methods?.includes(actualMethod) &&
    (code ? codeValid : !product.pickup_code_required) &&
    (pickupEmail ? emailValid : !product.email_pickup_link)
  async function checkout(accept = false) {
    if (!valid || busy || !user || count === undefined) return
    setBusy(true)
    setError(null)
    try {
      if (disclaimerNeeded && !accept) {
        setOpen(true)
        return
      }
      const version = disclaimer.data?.version
      if (disclaimerNeeded) {
        if (!version || !acknowledged || !read) return
        await storeApi.acceptDisclaimer(version)
        client.setQueryData(['store', 'disclaimer', user.id], {
          ...disclaimer.data,
          accepted: true,
        })
        setOpen(false)
      }
      const signature = JSON.stringify([
        user.id,
        product.id,
        quantity,
        actualMethod,
        code,
        pickupEmail,
      ])
      const requestKey = keys.current.get(signature) || storeRequestKey()
      keys.current.set(signature, requestKey)
      const created = await storeApi.checkout({
        product_id: product.id,
        quantity: count,
        payment_method: actualMethod as StorePaymentMethod,
        request_key: requestKey,
        ...(!product.official && version
          ? { disclaimer_version: version }
          : {}),
        ...(code ? { pickup_code: code } : {}),
        ...(pickupEmail ? { pickup_email: pickupEmail } : {}),
      })
      setResult(created)
      setCode('')
      setEmail('')
      keys.current.clear()
      await client.invalidateQueries({ queryKey: ['store', 'orders', user.id] })
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <aside className='bg-card space-y-4 rounded-lg border p-5 lg:sticky lg:top-24'>
      <div className='space-y-1'>
        <div className='text-xl font-semibold'>
          <StoreAmount quota={product.price_quota} />
        </div>
        <p className='text-muted-foreground text-xs'>
          {t('Unit price')} ·{' '}
          {t('Stock: {{count}}', { count: product.available_stock })}
        </p>
      </div>
      <StoreError error={error} />
      {product.trading_paused && (
        <p className='text-muted-foreground text-sm'>
          {t('This product or payment method is currently unavailable.')}
        </p>
      )}
      {result ? (
        <div className='space-y-3 border-t pt-4'>
          <h2 className='font-semibold'>
            {t(result.order.status === 'paid' ? 'Order paid' : 'Order created')}
          </h2>
          <p className='text-muted-foreground text-xs break-all'>
            {result.order.trade_no}
          </p>
          {result.order.status === 'pending' && !payment && (
            <Button
              className='w-full'
              disabled={busy}
              onClick={() => {
                setBusy(true)
                void storeApi
                  .pay(result.order.id)
                  .then((session) => {
                    setPayment(session)
                    if (session.status === 'paid') {
                      setResult((current) =>
                        current
                          ? {
                              ...current,
                              order: { ...current.order, status: 'paid' },
                            }
                          : current
                      )
                    }
                  })
                  .catch((issue) => setError(issue))
                  .finally(() => setBusy(false))
              }}
            >
              {t(
                actualMethod === 'balance'
                  ? 'Pay with balance'
                  : 'Prepare payment'
              )}
            </Button>
          )}
          {result.order.status === 'pending' &&
            payment?.status === 'pending' && (
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
                    void storeApi
                      .pay(result.order.id, payment.currency)
                      .then(async (current) => {
                        setPayment(
                          current.status === 'pending' ? current : null
                        )
                        if (current.status !== 'pending') {
                          const authoritative = await storeApi.order(
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
                      .catch((issue) => setError(issue))
                      .finally(() => setBusy(false))
                  }}
                >
                  {t('Continue to payment')}
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
          <StoreQuantityControl
            value={quantity}
            max={capacity}
            disabled={busy}
            onChange={setQuantity}
          />
          <fieldset className='space-y-2'>
            <legend className='mb-2 text-sm font-medium'>
              {t('Payment method')}
            </legend>
            {product.payment_methods?.map((item) => (
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
            {!product.payment_methods?.length && (
              <p className='text-muted-foreground text-sm'>
                {t('This seller has no available payment method.')}
              </p>
            )}
          </fieldset>
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
          <div className='flex justify-between border-t pt-4 text-sm'>
            <span>{t('Total')}</span>
            <strong>
              {total === undefined ? '—' : <StoreAmount quota={total} />}
            </strong>
          </div>
          {user ? (
            <Button
              className='w-full'
              disabled={
                !valid || busy || (disclaimerNeeded && !disclaimer.data)
              }
              onClick={() => void checkout()}
            >
              {t(busy ? 'Creating order...' : 'Place order')}
            </Button>
          ) : (
            <Button
              className='w-full'
              render={
                <a
                  href={`/sign-in?redirect=${encodeURIComponent(`/store/products/${product.id}`)}`}
                />
              }
            >
              {t('Sign in to buy')}
            </Button>
          )}
          {!product.official && (
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
                {t('Read purchase disclaimer')}
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
          <DialogTitle>{t('Non-official product disclaimer')}</DialogTitle>
          <DialogDescription>
            {t(
              'Read the complete disclaimer before your first purchase from an independent seller.'
            )}
          </DialogDescription>
          <div
            tabIndex={0}
            className='max-h-64 overflow-y-auto rounded-md border p-4 text-sm leading-7 whitespace-pre-wrap'
            onScroll={(event) => {
              const node = event.currentTarget
              if (node.scrollTop + node.clientHeight >= node.scrollHeight - 8) {
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
          {user && !disclaimer.data?.accepted && (
            <>
              <label className='flex items-start gap-3 text-sm'>
                <Checkbox
                  checked={acknowledged}
                  onCheckedChange={(value) => setAcknowledged(value === true)}
                  disabled={!read || !disclaimer.data}
                />
                <span>
                  {t('I have carefully read and agree to this disclaimer.')}
                </span>
              </label>
              <Button
                disabled={!read || !acknowledged || !valid || busy}
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
