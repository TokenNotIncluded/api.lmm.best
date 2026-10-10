/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { refreshCurrentAccount } from '@/features/onboarding/use-auth-user-refresh'
import { useAuthStore } from '@/stores/auth-store'

import { StoreAPIError, storeApi } from './api'
import { StoreDeliveryEmail } from './delivery-email'
import { StoreGuestOrders } from './guest-orders'
import { StoreRefundPanel } from './refund-panel'
import {
  CopyStoreValue,
  StoreAmount,
  StoreAuthGate,
  StoreError,
  StoreLoading,
} from './shared'
import { StoreContactButton } from './support-contact'
import type { StoreOrder, StorePaymentSession } from './types'
import {
  paymentLabel,
  continueStorePayment,
  safeStoreUrl,
  storeDate,
} from './utils'

export function StoreOrdersPage() {
  const user = useAuthStore((state) => state.auth.user)
  const config = useQuery({
    queryKey: ['store', 'config'],
    queryFn: storeApi.config,
    enabled: !user,
    retry: false,
  })
  if (!user && config.data?.store_access_supported === true) {
    return <StoreGuestOrders lookupSupported />
  }
  return (
    <StoreAuthGate>
      <StoreOrders />
    </StoreAuthGate>
  )
}
function StoreOrders() {
  const { t, i18n } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)!
  const [role, setRole] = useState<'buyer' | 'seller'>('buyer')
  const [page, setPage] = useState(1)
  const selectedId =
    new URLSearchParams(window.location.search).get('order') || ''
  const selectedValid = /^[a-zA-Z0-9-]{1,64}$/.test(selectedId)
  const selected = useQuery({
    queryKey: ['store', 'order', user.id, selectedId],
    queryFn: () => storeApi.order(selectedId),
    enabled: selectedValid,
    retry: false,
  })
  const query = useQuery({
    queryKey: ['store', 'orders', user.id, role, page],
    queryFn: () => storeApi.orders(role, page),
    retry: false,
  })
  return (
    <div className='space-y-5'>
      <div className='flex flex-wrap items-end justify-between gap-3'>
        <h1 className='console-page-title text-xl font-bold'>
          {t('Order history')}
        </h1>
        <Button
          variant='outline'
          size='sm'
          onClick={() => {
            void query.refetch()
            if (selectedValid) void selected.refetch()
            void refreshCurrentAccount()
          }}
        >
          {t('Refresh')}
        </Button>
      </div>
      <div className='flex gap-2'>
        <Button
          size='sm'
          variant={role === 'buyer' ? 'secondary' : 'ghost'}
          onClick={() => {
            setRole('buyer')
            setPage(1)
          }}
        >
          {t('My purchases')}
        </Button>
        <Button
          size='sm'
          variant={role === 'seller' ? 'secondary' : 'ghost'}
          onClick={() => {
            setRole('seller')
            setPage(1)
          }}
        >
          {t('My sales')}
        </Button>
      </div>
      {selectedId && (
        <section className='rounded-lg border'>
          <StoreError
            error={
              selectedValid ? selected.error : new Error('Store request failed')
            }
          />
          {selected.isFetching && !selected.data && <StoreLoading />}
          {selected.data && (
            <StoreOrderRow
              key={`${user.id}-${selected.data.id}-selected`}
              order={selected.data}
              buyer={selected.data.buyer_id === user.id}
              locale={i18n.language}
            />
          )}
        </section>
      )}
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {role === 'buyer' &&
        ((selected.data?.buyer_id === user.id &&
          selected.data.email_delivery_status === 'awaiting_verification') ||
          query.data?.items.some(
            (order) => order.email_delivery_status === 'awaiting_verification'
          )) && <StoreDeliveryEmail key={user.id} ownerId={user.id} />}
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data && (
          <>
            <div className='divide-y rounded-lg border'>
              {!query.data.items.length && (
                <p className='text-muted-foreground p-8 text-center text-sm'>
                  {t('No orders yet')}
                </p>
              )}
              {query.data.items
                .filter((order) => order.id !== selected.data?.id)
                .map((order) => (
                  <StoreOrderRow
                    key={`${user.id}-${order.id}-${role}`}
                    order={order}
                    buyer={role === 'buyer'}
                    locale={i18n.language}
                  />
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
                disabled={!query.data.has_more}
                onClick={() => setPage((value) => value + 1)}
              >
                {t('Next page')}
              </Button>
            </div>
          </>
        )
      )}
    </div>
  )
}
export function StoreOrderRow({
  order,
  buyer,
  locale,
}: {
  order: StoreOrder
  buyer: boolean
  locale: string
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)!
  const client = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [payment, setPayment] = useState<StorePaymentSession | null>(null)
  const [paymentIssuanceUncertain, setPaymentIssuanceUncertain] =
    useState(false)
  const [link, setLink] = useState('')
  const [error, setError] = useState<unknown>(null)
  useEffect(() => {
    if (!buyer || order.status !== 'pending') setPayment(null)
  }, [buyer, order.status])
  async function refreshOrders() {
    await Promise.all([
      client.invalidateQueries(
        { queryKey: ['store', 'orders', user.id] },
        { throwOnError: true }
      ),
      client.invalidateQueries(
        { queryKey: ['store', 'order', user.id, order.id] },
        { throwOnError: true }
      ),
      client.invalidateQueries({ queryKey: ['store', 'payments', user.id] }),
      ...(useAuthStore.getState().auth.user?.id === user.id
        ? [refreshCurrentAccount()]
        : []),
    ])
  }
  async function preparePayment() {
    setPaymentIssuanceUncertain(true)
    try {
      const session = await storeApi.pay(order.id, order.currency || undefined)
      setPayment(session)
    } catch (issue) {
      // A lost response may already have issued a payment obligation. Keep
      // cancellation unavailable until an authoritative read succeeds.
      if (
        !(issue instanceof StoreAPIError) ||
        issue.code !== 'STORE_PAYMENT_MINIMUM'
      ) {
        try {
          await refreshOrders()
          setPaymentIssuanceUncertain(false)
        } catch {
          /* Keep the original preparation error and the unresolved state. */
        }
      }
      throw issue
    }
    await refreshOrders()
    setPaymentIssuanceUncertain(false)
  }
  async function action(fn: () => Promise<void>) {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (issue) {
      setError(issue)
      if (
        issue instanceof StoreAPIError &&
        issue.code === 'STORE_PAYMENT_MINIMUM' &&
        issue.orderCancelled === true &&
        issue.orderStatus === 'cancelled' &&
        issue.orderId === order.id
      ) {
        setPayment(null)
        try {
          await refreshOrders()
          setPaymentIssuanceUncertain(false)
        } catch {
          /* Keep the confirmed minimum error and require a fresh order read. */
        }
      } else if (
        issue instanceof StoreAPIError &&
        issue.code === 'STORE_CONFLICT'
      ) {
        setPaymentIssuanceUncertain(true)
        try {
          await refreshOrders()
          setPaymentIssuanceUncertain(false)
        } catch {
          /* Preserve the conflict while cancellation remains unavailable. */
        }
      }
    } finally {
      setBusy(false)
    }
  }
  return (
    <article className='space-y-3 p-4'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='min-w-0 space-y-1'>
          <h2 className='font-semibold break-words'>{order.product_title}</h2>
          <p className='text-muted-foreground text-sm'>
            {order.variant_name || t('Historic/default variant')}
          </p>
          <p className='text-muted-foreground text-xs break-all'>
            {order.trade_no}
          </p>
          <p className='text-muted-foreground text-xs'>
            {storeDate(order.created_at, locale)} ·{' '}
            {t(paymentLabel(order.payment_method))}
          </p>
        </div>
        <div className='space-y-1 text-right text-sm'>
          <strong>
            <StoreAmount quota={order.price_quota} />
          </strong>
          <p className='text-muted-foreground'>
            {order.status === 'reconciliation_pending' &&
            order.payment_issue_code
              ? t('Payment confirmed; delivery needs review.')
              : t(order.status)}{' '}
            · {t('Quantity')}: {order.quantity}
          </p>
          {!buyer && (
            <p className='text-muted-foreground text-xs'>
              {t('Seller fee')}: <StoreAmount quota={order.fee_quota} />
            </p>
          )}
        </div>
      </div>
      <StoreError error={error} />
      {order.buyer_id > 0 && (
        <StoreContactButton orderId={order.id} buyer={buyer} />
      )}
      {buyer && (
        <div className='flex flex-wrap items-center gap-2'>
          {['paid', 'refund_pending'].includes(order.status) && (
            <Button
              size='sm'
              disabled={busy}
              onClick={() =>
                void action(async () => {
                  const result = await storeApi.pickupLink(order.id)
                  const url = safeStoreUrl(result.pickup_url)
                  if (!url) throw new Error('Pickup link is unavailable')
                  setLink(url)
                })
              }
            >
              {t('Get pickup link')}
            </Button>
          )}
          {order.status === 'pending' && (
            <>
              <Button
                size='sm'
                disabled={busy}
                onClick={() => void action(preparePayment)}
              >
                {t(
                  order.payment_method === 'balance'
                    ? 'Pay with balance'
                    : 'Prepare payment'
                )}
              </Button>
              {order.payment_issued === false && !paymentIssuanceUncertain && (
                <Button
                  size='sm'
                  variant='outline'
                  disabled={busy}
                  onClick={() =>
                    void action(async () => {
                      await storeApi.cancel(order.id)
                      await refreshOrders()
                    })
                  }
                >
                  {t('Cancel order')}
                </Button>
              )}
            </>
          )}
          {['pending', 'reconciliation_pending'].includes(order.status) && (
            <Button
              size='sm'
              variant='outline'
              disabled={busy}
              onClick={() =>
                void action(async () => {
                  await storeApi.reconcile(order.id)
                  await refreshOrders()
                  setPaymentIssuanceUncertain(false)
                })
              }
            >
              {t('Check payment status')}
            </Button>
          )}
        </div>
      )}
      {buyer && order.status === 'pending' && payment?.status === 'pending' && (
        <div className='flex flex-wrap items-center gap-3 text-sm'>
          <span>
            {t('Actual payment')}:{' '}
            <strong>
              {payment.amount} {payment.currency}
            </strong>
          </span>
          <Button
            size='sm'
            disabled={busy}
            onClick={() =>
              void action(async () => {
                const current = await storeApi.pay(order.id, payment.currency)
                setPayment(current.status === 'pending' ? current : null)
                if (current.status !== 'pending') {
                  await refreshOrders()
                  return
                }
                // A changed quote must be displayed for a fresh explicit confirmation.
                if (
                  current.amount_minor !== payment.amount_minor ||
                  current.currency !== payment.currency
                ) {
                  return
                }
                continueStorePayment(current)
              })
            }
          >
            {t('Continue to payment')}
          </Button>
        </div>
      )}
      {buyer && link && (
        <div className='bg-muted flex flex-wrap items-center justify-between gap-3 rounded-md p-3'>
          <a href={link} className='min-w-0 text-sm break-all underline'>
            {t('Open pickup page')}
          </a>
          <CopyStoreValue value={link} label='Copy pickup link' />
        </div>
      )}
      {buyer && order.email_pickup_link && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'The pickup link will be sent to the email provided at checkout. You can always retrieve the link here.'
          )}
        </p>
      )}
      {['paid', 'refund_pending', 'refunded'].includes(order.status) && (
        <StoreRefundPanel
          orderId={order.id}
          audience={buyer ? 'buyer' : 'seller'}
          onChanged={refreshOrders}
        />
      )}
    </article>
  )
}
