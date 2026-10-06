/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { storeApi } from './api'
import { StoreError, StoreLoading } from './shared'
import type { StoreOrderSummary, StorePage } from './types'
import { isStoreEmail, safeStoreUrl, storeDate } from './utils'

export function StoreOrderSearch({
  value,
  mode,
}: {
  value: string
  mode: 'order' | 'email'
}) {
  const { t, i18n } = useTranslation()
  const alive = useRef(true)
  const inFlight = useRef(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [code, setCode] = useState('')
  const [challenge, setChallenge] = useState('')
  const [challengeExpires, setChallengeExpires] = useState(0)
  const [resendAt, setResendAt] = useState(0)
  // This capability stays in this component's memory. Changing the search or
  // account unmounts the component; it must never enter a URL or shared cache.
  const [proof, setProof] = useState<{ token: string; expires: number } | null>(
    null
  )
  const [orders, setOrders] = useState<StorePage<StoreOrderSummary> | null>(
    null
  )
  const [now, setNow] = useState(Date.now())
  const [expired, setExpired] = useState(false)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  useEffect(() => {
    if (mode !== 'order') return
    let active = true
    setBusy(true)
    void storeApi
      .searchOrder(value)
      .then((order) => {
        if (active) {
          setOrders({ items: [order], offset: 0, limit: 1, has_more: false })
        }
      })
      .catch((issue) => {
        if (active) setError(issue)
      })
      .finally(() => {
        if (active) setBusy(false)
      })
    return () => {
      active = false
    }
  }, [mode, value])
  useEffect(() => {
    if (!challenge && !proof && !resendAt) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [challenge, proof, resendAt])
  useEffect(() => {
    if (proof && now >= proof.expires) {
      setProof(null)
      setOrders(null)
      setChallenge('')
      setCode('')
      setExpired(true)
    } else if (challenge && !proof && now >= challengeExpires) {
      setChallenge('')
      setCode('')
      setExpired(true)
    }
  }, [now, proof, challenge, challengeExpires])
  async function send() {
    if (inFlight.current || Date.now() < resendAt || !isStoreEmail(value)) {
      return
    }
    inFlight.current = true
    setBusy(true)
    setError(null)
    setExpired(false)
    setProof(null)
    setOrders(null)
    setChallenge('')
    setCode('')
    try {
      const result = await storeApi.sendOrderSearchEmailCode(value)
      if (!alive.current) return
      const time = Date.now()
      setNow(time)
      setChallenge(result.challenge_id)
      setChallengeExpires(time + result.expires_in * 1000)
      setResendAt(time + result.resend_after * 1000)
    } catch (issue) {
      if (alive.current) setError(issue)
    } finally {
      inFlight.current = false
      if (alive.current) setBusy(false)
    }
  }
  async function confirm(event: React.FormEvent) {
    event.preventDefault()
    if (
      inFlight.current ||
      !challenge ||
      !/^\d{6}$/.test(code) ||
      Date.now() >= challengeExpires
    ) {
      return
    }
    inFlight.current = true
    setBusy(true)
    setError(null)
    try {
      const result = await storeApi.confirmOrderSearchEmailCode(challenge, code)
      if (!alive.current) return
      const capability = {
        token: result.search_token,
        expires: Date.now() + result.expires_in * 1000,
      }
      setProof(capability)
      setCode('')
      const page = await storeApi.searchOrdersByVerifiedEmail(capability.token)
      if (alive.current && Date.now() < capability.expires) setOrders(page)
    } catch (issue) {
      if (alive.current) setError(issue)
    } finally {
      inFlight.current = false
      if (alive.current) setBusy(false)
    }
  }
  async function page(offset: number) {
    if (inFlight.current || !proof || Date.now() >= proof.expires) return
    inFlight.current = true
    setBusy(true)
    setError(null)
    const capability = proof
    try {
      const result = await storeApi.searchOrdersByVerifiedEmail(
        capability.token,
        offset
      )
      if (alive.current && Date.now() < capability.expires) setOrders(result)
    } catch (issue) {
      if (alive.current) setError(issue)
    } finally {
      inFlight.current = false
      if (alive.current) setBusy(false)
    }
  }
  const cooldown = Math.max(0, Math.ceil((resendAt - now) / 1000))
  return (
    <section className='space-y-4 border-y py-5' aria-label={t('Order lookup')}>
      <div className='space-y-1'>
        <h2 className='font-semibold'>
          {t(mode === 'email' ? 'Orders by email' : 'Order lookup')}
        </h2>
        <p className='text-muted-foreground text-sm break-all'>{value}</p>
      </div>
      <StoreError error={error} />
      {proof && !orders && !!error && (
        <Button
          type='button'
          variant='outline'
          disabled={busy}
          onClick={() => void page(0)}
          className='min-h-11'
        >
          {t('Retry')}
        </Button>
      )}
      {mode === 'email' && !proof && (
        <div className='max-w-xl space-y-3'>
          <p className='text-muted-foreground text-sm'>
            {t('Verify that you own this email before viewing its orders.')}
          </p>
          {!isStoreEmail(value) && (
            <StoreError error={new Error('Enter a valid email address')} />
          )}
          {expired && (
            <p role='status' className='text-muted-foreground text-sm'>
              {t('Verification expired. Send a new code to continue.')}
            </p>
          )}
          <Button
            type='button'
            variant='outline'
            disabled={busy || cooldown > 0 || !isStoreEmail(value)}
            onClick={() => void send()}
            className='min-h-11'
          >
            {t(
              cooldown > 0
                ? 'Wait {{seconds}} seconds'
                : challenge
                  ? 'Send another code'
                  : 'Send verification code',
              { seconds: cooldown }
            )}
          </Button>
          {challenge && (
            <form
              className='space-y-2'
              onSubmit={(event) => void confirm(event)}
            >
              <p role='status' className='text-muted-foreground text-xs'>
                {t(
                  'Verification code sent. Enter the six-digit code from this email.'
                )}
              </p>
              <Label htmlFor='store-order-search-code'>
                {t('Verification code')}
              </Label>
              <div className='flex gap-2'>
                <Input
                  id='store-order-search-code'
                  className='h-11 min-w-0'
                  inputMode='numeric'
                  autoComplete='one-time-code'
                  maxLength={6}
                  value={code}
                  onChange={(event) =>
                    setCode(event.target.value.replaceAll(/\D/g, ''))
                  }
                />
                <Button
                  type='submit'
                  className='min-h-11'
                  disabled={busy || !/^\d{6}$/.test(code)}
                >
                  {t('Verify and find orders')}
                </Button>
              </div>
            </form>
          )}
        </div>
      )}
      {busy && !orders && <StoreLoading />}
      {orders && (
        <>
          {!orders.items.length && (
            <p className='text-muted-foreground py-5 text-sm'>
              {t('No orders found')}
            </p>
          )}
          <div className='divide-y'>
            {orders.items.map((order) => {
              const pickupUrl = safeStoreUrl(order.pickup_url || '')
              return (
                <article key={order.id} className='space-y-2 py-4'>
                  <div className='flex flex-wrap justify-between gap-2'>
                    <h3 className='font-semibold break-words'>
                      {order.product_title}
                    </h3>
                    <span className='text-muted-foreground text-sm'>
                      {t(order.status)} · {t('Quantity')}: {order.quantity}
                    </span>
                  </div>
                  <p className='text-muted-foreground text-xs break-all'>
                    {order.trade_no}
                  </p>
                  <p className='text-muted-foreground text-xs'>
                    {storeDate(order.created_at, i18n.language)}
                  </p>
                  {pickupUrl ? (
                    <Button
                      size='sm'
                      variant='outline'
                      render={<a href={pickupUrl} />}
                    >
                      {t('Open pickup page')}
                    </Button>
                  ) : order.status === 'paid' ? (
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'To collect this order, verify the pickup email or sign in to the purchasing account.'
                      )}
                    </p>
                  ) : null}
                </article>
              )
            })}
          </div>
          {mode === 'email' && (
            <div className='flex justify-end gap-2'>
              <Button
                size='sm'
                variant='outline'
                disabled={busy || orders.offset <= 0}
                onClick={() =>
                  void page(Math.max(0, orders.offset - orders.limit))
                }
              >
                {t('Previous page')}
              </Button>
              <Button
                size='sm'
                variant='outline'
                disabled={busy || !orders.has_more}
                onClick={() => void page(orders.offset + orders.limit)}
              >
                {t('Next page')}
              </Button>
            </div>
          )}
        </>
      )}
    </section>
  )
}
