/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { catalogueApi } from './catalogue-api'
import { StoreCatalogueFiltersPanel } from './catalogue-filters'
import { StoreCatalogueProductCard } from './catalogue-product-card'
import { useStoreCatalogueSupport } from './catalogue-support'
import type { StoreCatalogueFilters } from './catalogue-types'
import { StoreCategorySelect } from './categories'
import { useStoreCategories } from './category-support'
import {
  readStoreCatalogueView,
  writeStoreCatalogueView,
} from './collection-storage'
import { StoreAnnouncement, StoreMerchantHomeHeader } from './merchant-home'
import { StoreMerchantIdentity } from './merchant-identity'
import { StoreOrderSearch } from './order-search'
import { StoreError, StoreLoading } from './shared'
import { useStoreViewer } from './store-viewer'
import { useStoreTrafficPage } from './traffic-page'

type SearchType = 'auto' | 'products' | 'order' | 'email'

export function StorePage({ sellerId }: { sellerId?: number } = {}) {
  const { t } = useTranslation()
  const [input, setInput] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [categoryId, setCategoryId] = useState('')
  const [searchType, setSearchType] = useState<SearchType>('auto')
  const [filters, setFilters] = useState<StoreCatalogueFilters>({
    sort: 'comprehensive',
  })
  const [view, setView] = useState(readStoreCatalogueView)
  const trafficPage = useStoreTrafficPage()
  const [lookup, setLookup] = useState<{
    mode: 'order' | 'email'
    value: string
  } | null>(null)
  const user = useAuthStore((state) => state.auth.user)
  const viewer = useStoreViewer()
  const support = useStoreCatalogueSupport()
  const categories = useStoreCategories(
    support.data?.store_categories_supported === true
  )
  const query = useQuery({
    queryKey: [
      'store',
      'products',
      viewer,
      search,
      page,
      sellerId,
      filters,
      categoryId,
      support.catalogueSupported,
    ],
    queryFn: ({ signal }) =>
      catalogueApi.products(
        {
          search,
          page,
          sellerId,
          ...(categories.data?.supported ? { categoryId } : {}),
          ...(support.catalogueSupported ? filters : {}),
        },
        signal
      ),
    retry: false,
  })
  return (
    <div className='flex flex-1 flex-col gap-5'>
      <StoreAnnouncement
        supported={support.data?.store_merchant_home_supported === true}
      />
      {sellerId && (
        <StoreMerchantHomeHeader
          sellerId={sellerId}
          supported={support.data?.store_merchant_home_supported === true}
        />
      )}
      <div className='flex min-h-28 flex-wrap items-end justify-between gap-4 border-b py-5'>
        <div className='relative z-10 space-y-1'>
          <h1 className='console-page-title text-xl font-bold'>
            {query.data?.seller &&
            support.data?.store_merchant_home_supported !== true
              ? t('Shop by {{name}}', {
                  name:
                    query.data.seller.display_name ||
                    query.data.seller.username,
                })
              : t('Browse products')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Explore digital products from the community and official sellers.'
            )}
          </p>
          {query.data?.seller &&
            support.data?.store_merchant_home_supported !== true && (
              <StoreMerchantIdentity seller={query.data.seller} />
            )}
          {sellerId && (
            <a
              href='/store'
              className='text-muted-foreground inline-block text-sm hover:underline'
            >
              {t('Browse all sellers')}
            </a>
          )}
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
      {!lookup && categories.data?.supported && (
        <StoreCategorySelect
          id='store-category-filter'
          all
          value={categoryId}
          items={categories.data.items}
          onChange={(value) => {
            setCategoryId(value)
            setPage(1)
          }}
        />
      )}
      {!lookup && (
        <StoreError
          error={categories.error}
          retry={() => void categories.refetch()}
        />
      )}
      {!lookup && support.catalogueSupported && (
        <StoreCatalogueFiltersPanel
          value={filters}
          onChange={(value) => {
            setFilters(value)
            setPage(1)
          }}
          view={view}
          onViewChange={(value) => {
            setView(value)
            writeStoreCatalogueView(value)
          }}
        />
      )}
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
                  <div
                    className={cn(
                      'grid gap-4',
                      view === 'cards' && 'grid-cols-12 sm:gap-5'
                    )}
                  >
                    {query.data.items.map((product, index) => (
                      <StoreCatalogueProductCard
                        key={product.id}
                        product={product}
                        trafficPage={trafficPage}
                        featured={index < 3}
                        list={view === 'list'}
                      />
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
