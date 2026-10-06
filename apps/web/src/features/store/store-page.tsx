/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { storeApi } from './api'
import { StoreAmount, StoreBadges, StoreError, StoreLoading } from './shared'
import { safeStoreUrl } from './utils'

export function StorePage() {
  const { t } = useTranslation()
  const [input, setInput] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: ['store', 'products', search, page],
    queryFn: () => storeApi.products(search, page),
    retry: false,
  })
  return (
    <div className='space-y-5'>
      <div className='flex flex-wrap items-end justify-between gap-4'>
        <div className='space-y-1'>
          <h1 className='console-page-title text-xl font-bold'>
            {t('Browse products')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Explore digital products from the community and official sellers.'
            )}
          </p>
        </div>
        <Button variant='outline' render={<a href='/store/manage' />}>
          {t('Sell a product')}
        </Button>
      </div>
      <form
        className='flex max-w-xl gap-2'
        onSubmit={(event) => {
          event.preventDefault()
          setSearch(input.trim())
          setPage(1)
        }}
      >
        <Input
          value={input}
          onChange={(event) => setInput(event.target.value)}
          aria-label={t('Search products')}
          placeholder={t('Search products')}
          maxLength={100}
        />
        <Button type='submit' variant='secondary'>
          {t('Search')}
        </Button>
      </form>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data && (
          <>
            {query.data.items.length === 0 ? (
              <p className='text-muted-foreground border-y py-12 text-center text-sm'>
                {t('No products found')}
              </p>
            ) : (
              <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
                {query.data.items.map((product) => (
                  <article
                    key={product.id}
                    className='bg-card overflow-hidden rounded-lg border'
                  >
                    <a
                      href={`/store/products/${product.id}`}
                      className='focus-visible:outline-ring block focus-visible:outline-2'
                    >
                      {safeStoreUrl(product.image_urls?.[0] || '') ? (
                        <img
                          src={safeStoreUrl(product.image_urls[0])}
                          alt={product.title}
                          className='aspect-[16/9] w-full object-cover'
                          loading='lazy'
                          referrerPolicy='no-referrer'
                        />
                      ) : null}
                      <div className='space-y-3 p-4'>
                        <StoreBadges product={product} />
                        <h2 className='truncate font-semibold'>
                          {product.title}
                        </h2>
                        <p className='text-muted-foreground line-clamp-2 min-h-10 text-sm'>
                          {product.description}
                        </p>
                        <div className='flex items-center justify-between gap-2 text-sm'>
                          <strong>
                            <StoreAmount quota={product.price_quota} />
                          </strong>
                          <span className='text-muted-foreground'>
                            {t('Stock: {{count}}', {
                              count: product.available_stock,
                            })}
                          </span>
                        </div>
                      </div>
                    </a>
                  </article>
                ))}
              </div>
            )}
            <div className='flex items-center justify-between border-t pt-4 text-sm'>
              <span className='text-muted-foreground'>
                {t('Page {{page}}', { page })}
              </span>
              <div className='flex gap-2'>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={page <= 1}
                  onClick={() => setPage((value) => value - 1)}
                >
                  {t('Previous page')}
                </Button>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={!query.data.has_more}
                  onClick={() => setPage((value) => value + 1)}
                >
                  {t('Next page')}
                </Button>
              </div>
            </div>
          </>
        )
      )}
      <div className='text-muted-foreground border-t pt-4 text-xs'>
        {t(
          'Official labels identify administrator-owned products. Other products are sold independently by their sellers.'
        )}
      </div>
    </div>
  )
}
