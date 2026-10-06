/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useMarketMoneyDraft } from '@/features/tool-market/money'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { useAuthStore } from '@/stores/auth-store'

import { storeApi } from './api'
import { StoreAmount, StoreAuthGate, StoreError, StoreLoading } from './shared'
import type { StoreProduct } from './types'
import { safeStoreUrl } from './utils'

export function StoreReviewPage() {
  return (
    <StoreAuthGate>
      <StoreReviews />
    </StoreAuthGate>
  )
}
function StoreReviews() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)!
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['store', 'reviews', user.id, page],
    queryFn: () => storeApi.reviews(page),
    enabled: user.role >= 10,
    retry: false,
  })
  const config = useQuery({
    queryKey: ['store', 'config'],
    queryFn: storeApi.config,
    enabled: user.role >= 10,
    retry: false,
  })
  if (user.role < 10) {
    return <StoreError error={new Error('Administrator access required')} />
  }
  return (
    <div className='space-y-5'>
      <h1 className='console-page-title text-xl font-bold'>
        {t('Review products')}
      </h1>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Administrators may review their own products. Approval publishes the product to the public store.'
        )}
      </p>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data && (
          <>
            <div className='divide-y rounded-lg border'>
              {!query.data.items.length && (
                <p className='text-muted-foreground p-8 text-center text-sm'>
                  {t('No products awaiting review')}
                </p>
              )}
              {query.data.items.map((product) => (
                <StoreReviewRow key={product.id} product={product} />
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
      {config.data && (
        <StorePromotionPrice
          key={config.data.promotion_quota}
          quota={config.data.promotion_quota}
        />
      )}
    </div>
  )
}
function StorePromotionPrice({ quota }: { quota: number }) {
  const { t } = useTranslation()
  const money = useWalletCurrency()
  const price = useMarketMoneyDraft(quota)
  const client = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  async function save(event: React.FormEvent) {
    event.preventDefault()
    if (!price.quota || busy) return
    setBusy(true)
    setError(null)
    try {
      await storeApi.savePromotionPrice(price.quota)
      await client.invalidateQueries({ queryKey: ['store', 'config'] })
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      onSubmit={(event) => void save(event)}
      className='space-y-3 border-t pt-5'
    >
      <Label htmlFor='admin-promotion-price'>
        {t('Monthly promotion price')} ({money.label})
      </Label>
      <div className='flex max-w-md gap-2'>
        <Input
          id='admin-promotion-price'
          inputMode='decimal'
          value={price.input}
          onChange={(event) => price.setInput(event.target.value)}
        />
        <Button disabled={!price.quota || busy}>{t('Save')}</Button>
      </div>
      <StoreError error={error} />
    </form>
  )
}
function StoreReviewRow({ product }: { product: StoreProduct }) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>(null)
  async function review(approved: boolean) {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await storeApi.review(product.id, approved, note)
      await client.invalidateQueries({ queryKey: ['store', 'reviews'] })
    } catch (issue) {
      setError(issue)
    } finally {
      setBusy(false)
    }
  }
  return (
    <article className='space-y-4 p-4'>
      <div className='flex flex-wrap justify-between gap-3'>
        <div>
          <h2 className='font-semibold'>{product.title}</h2>
          <p className='text-muted-foreground text-xs'>
            {t('Seller account ID')}: {product.seller_id}
          </p>
        </div>
        <strong className='text-sm'>
          <StoreAmount quota={product.price_quota} />
        </strong>
      </div>
      <p className='max-w-prose text-sm break-words whitespace-pre-wrap'>
        {product.description}
      </p>
      {product.image_urls?.length > 0 && (
        <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
          {product.image_urls
            .filter((url) => safeStoreUrl(url))
            .map((url, index) => (
              <img
                key={index}
                src={safeStoreUrl(url)}
                alt={product.title}
                className='aspect-video w-full rounded-md object-cover'
                loading='lazy'
                referrerPolicy='no-referrer'
              />
            ))}
        </div>
      )}
      <p className='text-muted-foreground text-xs'>
        {t('Seller contact')}: {product.contact}
      </p>
      {product.links?.map((link, index) => {
        const href = safeStoreUrl(link.url)
        return (
          href && (
            <a
              key={index}
              href={href}
              target='_blank'
              rel='noopener noreferrer'
              className='block text-sm underline'
            >
              {link.title || href} · {link.description}
            </a>
          )
        )
      })}
      <Textarea
        rows={2}
        value={note}
        maxLength={4000}
        aria-label={t('Review note')}
        placeholder={t('Review note')}
        onChange={(event) => setNote(event.target.value)}
      />
      <StoreError error={error} />
      <div className='flex gap-2'>
        <Button size='sm' disabled={busy} onClick={() => void review(true)}>
          {t('Approve')}
        </Button>
        <Button
          size='sm'
          variant='outline'
          disabled={busy}
          onClick={() => void review(false)}
        >
          {t('Reject')}
        </Button>
      </div>
    </article>
  )
}
