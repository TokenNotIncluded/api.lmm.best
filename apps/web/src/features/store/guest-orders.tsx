/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import { storeGuestApi } from './access-api'
import { STORE_ACCESS_COPY as copy } from './access-copy'
import {
  createStoreCheckoutIntentJournal,
  StoreCheckoutIntentError,
} from './checkout-intent'
import { isStoreCheckoutActorCurrent } from './checkout-recovery'
import {
  rememberStoreGuestOrder,
  storeGuestOrderIds,
} from './guest-order-storage'
import { readStoreGuestSession } from './guest-session'
import { StoreAmount, StoreAuthGate, StoreError } from './shared'
import type { StoreOrder, StorePaymentSession } from './types'
import { continueStorePayment, safeStoreUrl } from './utils'

export function StoreGuestOrders({
  lookupSupported = false,
}: { lookupSupported?: boolean } = {}) {
  const { t } = useTranslation()
  const [session] = useState(readStoreGuestSession)
  const [journal] = useState(() =>
    createStoreCheckoutIntentJournal({
      isActorCurrent: isStoreCheckoutActorCurrent,
    })
  )
  const [orders, setOrders] = useState<StoreOrder[]>([])
  const [error, setError] = useState<unknown>(null)
  const [busy, setBusy] = useState(false)
  const refreshing = useRef(false)
  const refreshAgain = useRef(false)
  const refresh = useCallback(
    async function refreshOrders() {
      if (!session) return
      if (refreshing.current) {
        refreshAgain.current = true
        return
      }
      refreshing.current = true
      setBusy(true)
      setError(null)
      try {
        const auth = useAuthStore.getState().auth
        const scope = { userId: auth.user?.id, sessionId: auth.session?.sid }
        const actor = { kind: 'guest' as const, guestId: session.guest_id }
        const recovered: StoreOrder[] = []
        if (lookupSupported) {
          const records = await journal.list(actor)
          for (const record of records.filter((item) => !item.superseded)) {
            const found = await journal.recover(
              actor,
              record.requestKey,
              async (key) => {
                const order = await storeGuestApi.lookup(
                  session.token,
                  key,
                  scope
                )
                if (order) recovered.push(order)
                return order ?? undefined
              }
            )
            if (found.kind === 'found') {
              rememberStoreGuestOrder(session.guest_id, found.order.id)
            }
          }
        }
        const result = await Promise.all(
          storeGuestOrderIds(session.guest_id).map((id) =>
            storeGuestApi.order(session.token, id, scope)
          )
        )
        if (isStoreCheckoutActorCurrent(actor)) {
          setOrders([
            ...new Map(
              [...result, ...recovered].map((order) => [order.id, order])
            ).values(),
          ])
        }
      } catch (issue) {
        setError(issue)
      } finally {
        refreshing.current = false
        if (refreshAgain.current) {
          refreshAgain.current = false
          await refreshOrders()
        } else {
          setBusy(false)
        }
      }
    },
    [session, journal, lookupSupported]
  )
  useEffect(() => {
    void refresh()
  }, [refresh])
  if (!session) return <StoreAuthGate>{null}</StoreAuthGate>
  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h1 className='console-page-title text-xl font-bold'>
          {t(copy.guestHistory)}
        </h1>
        <Button
          variant='outline'
          disabled={busy}
          onClick={() => void refresh()}
        >
          {t('Refresh')}
        </Button>
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(copy.guestHistoryHelp)}
      </p>
      <StoreError error={error} retry={() => void refresh()} />
      {!busy && !orders.length && !error && (
        <p className='text-muted-foreground text-sm'>{t(copy.guestEmpty)}</p>
      )}
      <div className='divide-y rounded-lg border'>
        {orders.map((order) => (
          <StoreGuestOrderRow
            key={order.id}
            order={order}
            token={session.token}
            guestId={session.guest_id}
            onChanged={refresh}
          />
        ))}
      </div>
    </div>
  )
}
export function StoreGuestOrderRow({
  order,
  token,
  guestId,
  onChanged,
}: {
  order: StoreOrder
  token: string
  guestId: string
  onChanged: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [payment, setPayment] = useState<StorePaymentSession | null>(null)
  const [pickup, setPickup] = useState('')
  async function action(fn: () => Promise<void>) {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <article className='space-y-3 p-4'>
      <div className='flex flex-wrap justify-between gap-3'>
        <div className='min-w-0 space-y-1'>
          <h2 className='font-semibold break-words'>{order.product_title}</h2>
          <p className='text-muted-foreground text-sm'>
            {order.variant_name || t('Historic/default variant')}
          </p>
          <p className='text-muted-foreground text-xs break-all'>
            {order.trade_no}
          </p>
        </div>
        <div className='space-y-1 text-right text-sm'>
          <StoreAmount quota={order.price_quota} />
          <p>
            {t(order.status)} · {t('Quantity')}: {order.quantity}
          </p>
        </div>
      </div>
      <StoreError error={error} />
      <div className='flex flex-wrap gap-2'>
        {['pending', 'reconciliation_pending'].includes(order.status) && (
          <Button
            size='sm'
            variant='outline'
            disabled={busy}
            onClick={() =>
              void action(async () => {
                const actor = { kind: 'guest' as const, guestId }
                if (!isStoreCheckoutActorCurrent(actor)) {
                  throw new StoreCheckoutIntentError('actor-changed')
                }
                const auth = useAuthStore.getState().auth
                const scope = {
                  userId: auth.user?.id,
                  sessionId: auth.session?.sid,
                }
                await storeGuestApi.reconcile(token, order.id, scope)
                if (!isStoreCheckoutActorCurrent(actor)) return
                setPayment(null)
                await onChanged()
              })
            }
          >
            {t('Check payment status')}
          </Button>
        )}
        {order.status === 'pending' && (
          <>
            <Button
              size='sm'
              disabled={busy}
              onClick={() =>
                void action(async () => {
                  const result = await storeGuestApi.pay(
                    token,
                    order.id,
                    order.currency || undefined
                  )
                  setPayment(result.status === 'pending' ? result : null)
                  await onChanged()
                })
              }
            >
              {t('Prepare payment')}
            </Button>
            {order.payment_issued === false && (
              <Button
                size='sm'
                variant='outline'
                disabled={busy}
                onClick={() =>
                  void action(async () => {
                    await storeGuestApi.cancel(token, order.id)
                    setPayment(null)
                    await onChanged()
                  })
                }
              >
                {t('Cancel order')}
              </Button>
            )}
          </>
        )}
        {['paid', 'refund_pending'].includes(order.status) && (
          <Button
            size='sm'
            disabled={busy}
            onClick={() =>
              void action(async () => {
                const result = await storeGuestApi.pickupLink(token, order.id)
                const url = safeStoreUrl(result.pickup_url)
                if (!url) throw new Error('Pickup link is unavailable')
                setPickup(url)
              })
            }
          >
            {t('Get pickup link')}
          </Button>
        )}
      </div>
      {order.status === 'pending' && payment?.status === 'pending' && (
        <div className='flex flex-wrap items-center gap-3 text-sm'>
          <span>
            {t('Actual payment')}: {payment.amount} {payment.currency}
          </span>
          <Button
            size='sm'
            disabled={busy}
            onClick={() =>
              void action(async () => {
                const current = await storeGuestApi.pay(
                  token,
                  order.id,
                  payment.currency
                )
                setPayment(current.status === 'pending' ? current : null)
                if (current.status !== 'pending') {
                  await onChanged()
                  return
                }
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
      {pickup && (
        <Button size='sm' variant='outline' render={<a href={pickup} />}>
          {t('View order and collect items')}
        </Button>
      )}
    </article>
  )
}
