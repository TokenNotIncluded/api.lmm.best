/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQuery } from '@tanstack/react-query'
/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { ArrowUpRight, Search } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
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
    <div className='flex min-w-0 flex-1 flex-col gap-4 sm:gap-7'>
      <StoreAnnouncement
        supported={support.data?.store_merchant_home_supported === true}
      />
      {sellerId && (
        <StoreMerchantHomeHeader
          sellerId={sellerId}
          supported={support.data?.store_merchant_home_supported === true}
        />
      )}
      <div className='flex items-start justify-between gap-4 pt-3 pb-2 sm:items-end sm:pt-8 sm:pb-4'>
        <div className='relative z-10 flex min-w-0 flex-1 flex-col gap-2'>
          <h1 className='console-page-title text-3xl font-semibold tracking-tight sm:text-4xl'>
            {query.data?.seller &&
            support.data?.store_merchant_home_supported !== true
              ? t('Shop by {{name}}', {
                  name:
                    query.data.seller.display_name ||
                    query.data.seller.username,
                })
              : t('Browse products')}
          </h1>
          <p className='text-muted-foreground sr-only max-w-md text-sm leading-6 sm:not-sr-only'>
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
          variant='ghost'
          className='relative z-10 h-11 shrink-0 gap-1 rounded-full px-2 text-xs sm:px-4 sm:text-sm'
          render={<a href='/store/manage' />}
        >
          {t('Sell a product')}
          <ArrowUpRight className='size-4' aria-hidden='true' />
        </Button>
      </div>
      <form
        role='search'
        className='bg-muted/60 focus-within:ring-ring/50 flex min-w-0 items-center gap-2 rounded-2xl py-1.5 ps-4 pe-1.5 focus-within:ring-2'
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
        <Search
          className='text-muted-foreground size-5 shrink-0'
          aria-hidden='true'
        />
        <Input
          type='search'
          className='h-11 min-w-0 flex-1 rounded-none border-0 bg-transparent px-0 text-base shadow-none ring-0 focus-visible:ring-0 dark:bg-transparent'
          value={input}
          onChange={(event) => {
            setInput(event.target.value)
            setLookup(null)
          }}
          aria-label={t('Search products, order number or email')}
          placeholder={t('Search products, order number or email')}
          maxLength={254}
        />
        <Button type='submit' className='h-11 shrink-0 rounded-xl px-4'>
          {t('Search')}
        </Button>
      </form>
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
        supported={!lookup && support.catalogueSupported}
        extraActive={categoryId !== '' || searchType !== 'auto'}
      >
        <FieldGroup className='grid gap-4 sm:grid-cols-2'>
          <Field>
            <FieldLabel htmlFor='store-search-type'>
              {t('Search type')}
            </FieldLabel>
            <select
              id='store-search-type'
              aria-label={t('Search type')}
              className='bg-background h-11 w-full rounded-xl border px-3 text-base sm:text-sm'
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
          </Field>
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
        </FieldGroup>
      </StoreCatalogueFiltersPanel>
      {!lookup && (
        <StoreError
          error={categories.error}
          retry={() => void categories.refetch()}
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
                  <div className='flex flex-1 flex-col items-center justify-center gap-6 px-4 py-16 text-center'>
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
                      'grid gap-x-4 gap-y-0 [&>article+article]:border-t [&>article+article]:border-border/45 sm:gap-y-8 sm:[&>article+article]:border-t-0',
                      view === 'cards' && 'grid-cols-12 sm:gap-x-8 lg:gap-x-12'
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
                  <div className='flex flex-wrap items-center justify-between gap-3 pt-4 text-sm'>
                    <span className='text-muted-foreground'>
                      {t('Page {{page}}', { page })}
                    </span>
                    <div className='flex gap-2'>
                      <Button
                        variant='outline'
                        size='sm'
                        className='h-11'
                        disabled={page <= 1}
                        onClick={() => setPage((value) => value - 1)}
                      >
                        {t('Previous page')}
                      </Button>
                      <Button
                        variant='outline'
                        size='sm'
                        className='h-11'
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
      <div className='text-muted-foreground mt-auto pt-6 pb-2 text-xs leading-5'>
        {t(
          'Official labels identify administrator-owned products. Other products are sold independently by their sellers.'
        )}
      </div>
    </div>
  )
}
