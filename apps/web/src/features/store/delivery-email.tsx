/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { storeApi } from './api'
import { StoreError, StoreLoading } from './shared'

export function StoreDeliveryEmail({ ownerId }: { ownerId: number }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['store', 'delivery-email', ownerId],
    queryFn: storeApi.deliveryEmailStatus,
    retry: false,
  })
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)
  const [cooldown, setCooldown] = useState(0)
  const [error, setError] = useState<unknown>(null)
  useEffect(() => {
    if (cooldown <= 0) return
    const timer = window.setTimeout(
      () => setCooldown((value) => Math.max(0, value - 1)),
      1000
    )
    return () => window.clearTimeout(timer)
  }, [cooldown])
  async function send() {
    if (busy || cooldown) return
    setBusy(true)
    setError(null)
    try {
      const result = await storeApi.sendDeliveryEmailVerification()
      if (result.sent !== true) throw new Error('Store request failed')
      setSent(true)
      setCooldown(60)
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  async function confirm(event: React.FormEvent) {
    event.preventDefault()
    if (busy || !/^\d{6}$/.test(code)) return
    setBusy(true)
    setError(null)
    try {
      await storeApi.confirmDeliveryEmailVerification(code)
      setCode('')
      await query.refetch()
      await client.invalidateQueries({ queryKey: ['store', 'orders', ownerId] })
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className='bg-muted space-y-3 rounded-md p-4'>
      <h2 className='text-sm font-semibold'>{t('Verify delivery email')}</h2>
      <StoreError
        error={query.error || error}
        retry={query.error ? () => void query.refetch() : undefined}
      />
      {query.isPending ? (
        <StoreLoading />
      ) : query.data?.verified ? (
        <p className='text-success text-sm'>
          {t('Email verified')}
          {query.data.email ? ` · ${query.data.email}` : ''}
        </p>
      ) : !query.data ? null : !query.data.email ? (
        <p className='text-muted-foreground text-sm'>
          {t('Set an email in your profile, then return here to verify it.')}{' '}
          <a href='/profile' className='underline'>
            {t('Profile')}
          </a>
        </p>
      ) : (
        <>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Verify your account email to receive pickup links. Web pickup remains available.'
            )}
          </p>
          <p className='text-sm'>
            {t('Account email')}: {query.data.email}
          </p>
          <Button
            size='sm'
            variant='outline'
            disabled={busy || cooldown > 0}
            onClick={() => void send()}
          >
            {t(
              cooldown > 0
                ? 'Wait {{seconds}} seconds'
                : sent
                  ? 'Send another code'
                  : 'Send verification code',
              { seconds: cooldown }
            )}
          </Button>
          {sent && (
            <form
              onSubmit={(event) => void confirm(event)}
              className='space-y-2'
            >
              <p role='status' className='text-muted-foreground text-xs'>
                {t('Verification code sent to your account email.')}
              </p>
              <Label htmlFor='delivery-email-code'>
                {t('Verification code')}
              </Label>
              <div className='flex max-w-sm gap-2'>
                <Input
                  id='delivery-email-code'
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
                  size='sm'
                  disabled={busy || !/^\d{6}$/.test(code)}
                >
                  {t('Confirm email')}
                </Button>
              </div>
            </form>
          )}
        </>
      )}
    </section>
  )
}
