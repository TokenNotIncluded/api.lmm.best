/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ForgeShaderSurface } from '@/components/shaders/forge-shader-surface'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useAuthStore } from '@/stores/auth-store'

import { storeApi } from './api'
import { StoreOrderSearch } from './order-search'
import { StoreBadges, StoreError, StoreLoading } from './shared'
import { safeStoreUrl } from './utils'
import { StoreProductPrice } from './variant-summary'

type SearchType = 'auto' | 'products' | 'order' | 'email'

export function StorePage() {
  const { t } = useTranslation()
  const [input, setInput] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [searchType, setSearchType] = useState<SearchType>('auto')
  const [lookup, setLookup] = useState<{
    mode: 'order' | 'email'
    value: string
  } | null>(null)
  const user = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: ['store', 'products', search, page],
    queryFn: () => storeApi.products(search, page),
    retry: false,
  })
  return (
    <div className='flex flex-1 flex-col gap-5'>
      <div className='relative isolate flex min-h-28 flex-wrap items-end justify-between gap-4 overflow-hidden border-b py-5'>
        <div className='pointer-events-none absolute inset-y-0 end-0 w-2/5'>
          <ForgeShaderSurface variant='store' className='opacity-40' />
        </div>
        <div className='relative z-10 space-y-1'>
          <h1 className='console-page-title text-xl font-bold'>
            {t('Browse products')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Explore digital products from the community and official sellers.'
            )}
          </p>
        </div>
        <Button
          variant='outline'
          className='relative z-10'
          render={<a href='/store/manage' />}
        >
          {t('Sell a product')}
        </Button>
      </div>
      <form
        className='grid max-w-2xl grid-cols-[minmax(0,1fr)_auto] gap-2 sm:grid-cols-[auto_minmax(0,1fr)_auto]'
        onSubmit={(event) => {
          event.preventDefault()
          const value = input.trim()
          const mode =
            searchType === 'auto'
              ? value.includes('@')
                ? 'email'
                : /^MS[a-zA-Z0-9]{30}$/.test(value)
                  ? 'order'
                  : 'products'
              : searchType
          setLookup(value && mode !== 'products' ? { mode, value } : null)
          if (mode === 'products') setSearch(value)
          setPage(1)
        }}
      >
        <select
          aria-label={t('Search type')}
          className='bg-background col-span-2 h-11 rounded-md border px-3 text-sm sm:col-span-1'
          value={searchType}
          onChange={(event) => {
            setSearchType(event.target.value as SearchType)
            setLookup(null)
          }}
        >
          <option value='auto'>{t('Auto detect')}</option>
          <option value='products'>{t('Products')}</option>
          <option value='order'>{t('Order number')}</option>
          <option value='email'>{t('Email')}</option>
        </select>
        <Input
          type='search'
          className='h-11 min-w-0'
          value={input}
          onChange={(event) => {
            setInput(event.target.value)
            setLookup(null)
          }}
          aria-label={t('Search products, order number or email')}
          placeholder={t('Search products, order number or email')}
          maxLength={254}
        />
        <Button type='submit' variant='secondary' className='h-11'>
          {t('Search')}
        </Button>
      </form>
      {lookup ? (
        <StoreOrderSearch
          key={`${lookup.mode}-${lookup.value}-${user?.id || 'guest'}`}
          value={lookup.value}
          mode={lookup.mode}
        />
      ) : (
        <>
          <StoreError error={query.error} retry={() => void query.refetch()} />
          {query.isPending ? (
            <StoreLoading />
          ) : (
            query.data && (
              <>
                {query.data.items.length === 0 ? (
                  <div className='flex flex-1 flex-col items-center justify-center gap-6 border-t px-4 py-10 text-center'>
                    <div className='max-w-md space-y-2'>
                      <h2 className='font-semibold'>
                        {t(
                          !search && page === 1
                            ? 'Nothing on the shelves yet.'
                            : 'No products found'
                        )}
                      </h2>
                      {!search && page === 1 ? (
                        <p className='text-muted-foreground text-sm'>
                          {t(
                            'Published products will appear here. You can be the first seller.'
                          )}
                        </p>
                      ) : (
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={() => {
                            setInput('')
                            setSearch('')
                            setPage(1)
                          }}
                        >
                          {t('Clear search')}
                        </Button>
                      )}
                    </div>
                  </div>
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
                                <StoreProductPrice product={product} />
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
                {(query.data.items.length > 0 ||
                  page > 1 ||
                  query.data.has_more) && (
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
                )}
              </>
            )
          )}
        </>
      )}
      <div className='text-muted-foreground mt-auto border-t pt-4 text-xs'>
        {t(
          'Official labels identify administrator-owned products. Other products are sold independently by their sellers.'
        )}
      </div>
    </div>
  )
}
