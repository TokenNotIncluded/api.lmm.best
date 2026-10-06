/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Markdown } from '@/components/ui/markdown'
import { useAuthStore } from '@/stores/auth-store'

import { storeApi } from './api'
import { StoreClaimItems } from './claim-items'
import { CopyStoreValue, StoreError, StoreLoading } from './shared'
import type { StoreClaim } from './types'
import { safeStoreUrl } from './utils'

export function StoreClaimPage({ token }: { token: string }) {
  const user = useAuthStore((state) => state.auth.user)
  return (
    <StoreClaimContent key={`${token}-${user?.id || 'guest'}`} token={token} />
  )
}
function StoreClaimContent({ token }: { token: string }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: ['store', 'claim-metadata', token, user?.id || 'guest'],
    queryFn: () => storeApi.claimMetadata(token),
    retry: false,
    gcTime: 0,
  })
  const [code, setCode] = useState('')
  const [claim, setClaim] = useState<StoreClaim | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  useEffect(() => {
    const meta = document.createElement('meta')
    meta.name = 'referrer'
    meta.content = 'no-referrer'
    document.head.append(meta)
    return () => {
      meta.remove()
    }
  }, [])
  async function collect(event: React.FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const result = await storeApi.claim(token, code)
      setClaim(result)
      setCode('')
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  if (query.isPending) return <StoreLoading />
  if (!query.data) {
    return (
      <StoreError
        error={query.error || new Error('Pickup link is unavailable')}
        retry={() => void query.refetch()}
      />
    )
  }
  const metadata = query.data
  return (
    <div className='space-y-6'>
      <div className='space-y-2'>
        <h1 className='console-page-title text-xl font-bold'>
          {t('Collect your items')}
        </h1>
        <h2 className='text-lg break-words'>{metadata.product_title}</h2>
        <p className='text-muted-foreground text-sm break-words'>
          {t('Specification')}:{' '}
          {claim?.variant_name ||
            metadata.variant_name ||
            t('Historic/default variant')}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Keep this link private. Anyone meeting its protection requirements can collect the items.'
          )}
        </p>
      </div>
      <StoreError error={error} />
      {metadata.status !== 'paid' ? (
        <p className='rounded-lg border p-4 text-sm'>
          {t('This order is not paid yet. Check its status in order history.')}
        </p>
      ) : metadata.pickup_login_required && !metadata.pickup_login_satisfied ? (
        <div className='space-y-3 rounded-lg border p-4'>
          <p className='text-sm'>
            {t('Sign in with the purchasing account to collect this order.')}
          </p>
          <p className='text-muted-foreground text-sm leading-6'>
            {t(
              'If your IP cannot sign in and you have no valid session, sign in from an allowed network first. For collection from any IP, sellers can turn off account-only collection and require a pickup code instead.'
            )}
          </p>
          <CopyStoreValue
            value={window.location.href}
            label='Copy collection link'
          />
          <Button
            render={
              <a
                href={`/sign-in?redirect=${encodeURIComponent(window.location.pathname)}`}
              />
            }
          >
            {t('Sign in')}
          </Button>
        </div>
      ) : claim ? (
        <div className='flex flex-col gap-6'>
          {claim.product_description && (
            <Markdown className='min-w-0'>{claim.product_description}</Markdown>
          )}
          {!!claim.product_links?.length && (
            <div className='flex flex-col gap-3'>
              <h3 className='text-sm font-semibold'>{t('Product links')}</h3>
              {claim.product_links.map((link, index) => {
                const url = safeStoreUrl(link.url)
                if (!url) return null
                return (
                  <div key={index} className='flex flex-col gap-1 text-sm'>
                    <a
                      href={url}
                      target='_blank'
                      rel='noopener noreferrer'
                      className='focus-visible:outline-ring underline underline-offset-4 focus-visible:outline-2'
                    >
                      {link.title || url}
                    </a>
                    {link.description && (
                      <p className='text-muted-foreground break-words whitespace-pre-wrap'>
                        {link.description}
                      </p>
                    )}
                  </div>
                )
              })}
            </div>
          )}
          <StoreClaimItems
            key={claim.order_id}
            items={claim.items}
            deliveryTemplate={claim.delivery_template}
            variantName={claim.variant_name || metadata.variant_name}
          />
        </div>
      ) : (
        <form
          onSubmit={(event) => void collect(event)}
          className='space-y-4 rounded-lg border p-5'
        >
          {metadata.pickup_code_required && (
            <div className='space-y-2'>
              <Label htmlFor='claim-code'>{t('Pickup code')}</Label>
              <Input
                id='claim-code'
                type='password'
                value={code}
                maxLength={72}
                autoComplete='off'
                required
                onChange={(event) => setCode(event.target.value)}
              />
              {new TextEncoder().encode(code).length > 72 && (
                <p role='alert' className='text-destructive text-xs'>
                  {t(
                    'Pickup code is too long. Please shorten it and try again.'
                  )}
                </p>
              )}
            </div>
          )}
          <Button
            type='submit'
            disabled={
              busy ||
              (metadata.pickup_code_required &&
                (!code || new TextEncoder().encode(code).length > 72))
            }
          >
            {t(busy ? 'Collecting...' : 'Collect items')}
          </Button>
        </form>
      )}
      <a
        href='/store/orders'
        className='text-muted-foreground inline-block text-sm underline'
      >
        {t('Order history')}
      </a>
    </div>
  )
}
